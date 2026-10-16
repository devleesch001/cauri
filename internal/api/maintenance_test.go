package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/jobs"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// The administration of the database: what it holds, the simulation of a
// purge (GET) and the purge (DELETE) as a task followed to its end, the
// compaction, the configuration; all behind the session, changes behind
// the header too.
func TestAdminMaintenance(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, func(c *api.Config) {
			c.Admin = auth.NewAdmin("s3cret")
			c.Options = []envflag.Option{{Name: "listen", Env: "CAURI_LISTEN", Default: ":8080", Value: ":9090", Source: envflag.FromEnv}}
			c.UnknownEnv = []string{"CAURI_LISTN"}
		})
		if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
			t.Fatal(err)
		}
		login := adminDo(t, e, "POST", "/api/admin/login", `{"password":"s3cret"}`, nil, true)
		if len(login.cookies) != 1 {
			t.Fatalf("login = %d", login.code)
		}
		session := login.cookies[0]
		call := func(method, path string, header bool, out any) int {
			t.Helper()
			r := adminDo(t, e, method, path, "", session, header)
			if out != nil && r.code < 300 {
				if err := json.Unmarshal(r.body, out); err != nil {
					t.Fatalf("%s %s: %v: %s", method, path, err, r.body)
				}
			}
			return r.code
		}
		// follow polls a task until it ends.
		follow := func(id int64) jobs.Job {
			t.Helper()
			for range 500 {
				var j jobs.Job
				if c := call("GET", "/api/admin/jobs/"+strings.TrimSpace(jsonOf(id)), false, &j); c != http.StatusOK {
					t.Fatalf("task %d = %d", id, c)
				}
				if j.Status != jobs.Running {
					return j
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("task %d still running", id)
			return jobs.Job{}
		}

		if r := adminDo(t, e, "GET", "/api/admin/database", "", nil, false); r.code != http.StatusUnauthorized {
			t.Errorf("database without a session = %d", r.code)
		}
		var d struct {
			Engine string            `json:"engine"`
			Schema string            `json:"schema"`
			Size   int64             `json:"size"`
			Tables []store.TableInfo `json:"tables"`
			Raw    store.RawInfo     `json:"raw"`
		}
		if c := call("GET", "/api/admin/database", false, &d); c != http.StatusOK || d.Size == 0 || d.Schema == "" || d.Raw.Lines != 4 {
			t.Fatalf("database = %d %+v", c, d)
		}
		found := false
		for _, tb := range d.Tables {
			found = found || tb.Name == "sessions" && tb.Rows == 1 && tb.Bytes > 0
		}
		if !found {
			t.Errorf("tables = %+v", d.Tables)
		}

		var cfg struct {
			Options    []envflag.Option `json:"options"`
			UnknownEnv []string         `json:"unknown_env"`
		}
		if c := call("GET", "/api/admin/config", false, &cfg); c != http.StatusOK || len(cfg.Options) != 1 ||
			cfg.Options[0].Source != envflag.FromEnv || len(cfg.UnknownEnv) != 1 {
			t.Errorf("config = %d %+v", c, cfg)
		}

		// Simulation: nothing changes; no criteria, nothing at all.
		if c := call("GET", "/api/admin/sessions", false, nil); c != http.StatusBadRequest {
			t.Errorf("purge of everything = %d", c)
		}
		var counts store.PurgeCounts
		if c := call("GET", "/api/admin/sessions?sensor=hp-01", false, &counts); c != http.StatusOK || counts.Sessions != 1 || counts.Events != 4 {
			t.Fatalf("simulated purge = %d %+v", c, counts)
		}
		var raw store.RawInfo
		if c := call("GET", "/api/admin/raw-events?older_than_days=0", false, &raw); c != http.StatusOK || raw.Lines != 4 {
			t.Errorf("simulated raw purge = %d %+v", c, raw)
		}
		if c := call("GET", "/api/admin/raw-events?older_than_days=-1", false, nil); c != http.StatusBadRequest {
			t.Errorf("negative days = %d", c)
		}

		// The raw lines go first, then the sessions, then the file shrinks.
		if c := call("DELETE", "/api/admin/raw-events?older_than_days=0", false, nil); c != http.StatusForbidden {
			t.Errorf("purge without the header = %d", c)
		}
		var j jobs.Job
		if c := call("DELETE", "/api/admin/raw-events?older_than_days=0", true, &j); c != http.StatusAccepted || j.Status != jobs.Running {
			t.Fatalf("raw purge = %d %+v", c, j)
		}
		if j = follow(j.ID); j.Status != jobs.Done || j.Progress.Done != 4 {
			t.Errorf("raw purge task = %+v", j)
		}
		if c := call("DELETE", "/api/admin/sessions?sensor=hp-01", true, &j); c != http.StatusAccepted {
			t.Fatalf("purge = %d", c)
		}
		if j = follow(j.ID); j.Status != jobs.Done || j.Progress.Done != 1 || j.Progress.Total != 1 {
			t.Errorf("purge task = %+v", j)
		}
		if c := call("GET", "/api/admin/sessions?sensor=hp-01", false, &counts); c != http.StatusOK || counts.Sessions != 0 {
			t.Errorf("after the purge = %+v", counts)
		}
		if c := call("POST", "/api/admin/database/vacuum", true, &j); c != http.StatusAccepted || j.Cancelable {
			t.Fatalf("vacuum = %d %+v", c, j)
		}
		if j = follow(j.ID); j.Status != jobs.Done {
			t.Errorf("vacuum task = %+v", j)
		}
		var list []jobs.Job
		if c := call("GET", "/api/admin/jobs", false, &list); c != http.StatusOK || len(list) != 3 || list[0].Kind != "vacuum" {
			t.Errorf("tasks = %+v", list)
		}
		if c := call("GET", "/api/admin/jobs/999", false, nil); c != http.StatusNotFound {
			t.Errorf("unknown task = %d", c)
		}
	})
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
