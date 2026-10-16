package api_test

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/geoip/geoiptest"
	"github.com/devleesch001/cauri/internal/jobs"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// A database configured but missing is downloaded on request, as a task,
// then loaded: GET tells its state, POST starts the download.
func TestAdminDownloads(t *testing.T) {
	built := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	file, err := os.ReadFile(geoiptest.Write(t, t.TempDir(), "DBIP-City-Lite", built,
		geoiptest.Network{CIDR: "203.0.113.0/24", Country: "FR"}))
	if err != nil {
		t.Fatal(err)
	}
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(file) }))
	defer mirror.Close()

	geo, err := geoip.Open(filepath.Join(t.TempDir(), "geoip.mmdb"), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = geo.Close() }()
	e := setup(t, storetest.SQLite(t), func(c *api.Config) {
		c.Admin = auth.NewAdmin("s3cret")
		c.GeoIP = geo
		c.Downloads = []api.Download{{Name: "geoip", URL: "http://user:pw@" + mirror.Listener.Addr().String() + "/geo.mmdb", DB: geo}}
	})
	session := adminDo(t, e, "POST", "/api/admin/login", `{"password":"s3cret"}`, nil, true).cookies[0]
	get := func(path string, out any) {
		t.Helper()
		r := adminDo(t, e, "GET", path, "", session, false)
		if r.code != http.StatusOK || json.Unmarshal(r.body, out) != nil {
			t.Fatalf("GET %s = %d %s", path, r.code, r.body)
		}
	}
	type state struct {
		Name   string      `json:"name"`
		URLs   []string    `json:"urls"`
		Loaded *geoip.Info `json:"loaded"`
		Size   *int64      `json:"size"`
	}
	var list []state
	get("/api/admin/downloads", &list)
	if len(list) != 1 || list[0].Loaded != nil || list[0].Size != nil || list[0].URLs[0] != "http://user:xxxxx@"+mirror.Listener.Addr().String()+"/geo.mmdb" {
		t.Fatalf("before = %+v", list)
	}
	var about struct {
		GeoIP *geoip.Info `json:"geoip"`
	}
	get("/api/about", &about)
	if about.GeoIP != nil {
		t.Errorf("about names a database not loaded: %+v", about.GeoIP)
	}

	if r := adminDo(t, e, "POST", "/api/admin/downloads/geoip", "", session, false); r.code != http.StatusForbidden {
		t.Errorf("download without the header = %d", r.code)
	}
	if r := adminDo(t, e, "POST", "/api/admin/downloads/other", "", session, true); r.code != http.StatusNotFound {
		t.Errorf("unknown database = %d", r.code)
	}
	r := adminDo(t, e, "POST", "/api/admin/downloads/geoip", "", session, true)
	var j jobs.Job
	if r.code != http.StatusAccepted || json.Unmarshal(r.body, &j) != nil || j.Kind != "download-geoip" {
		t.Fatalf("download = %d %s", r.code, r.body)
	}
	for j.Status == jobs.Running {
		time.Sleep(10 * time.Millisecond)
		get(fmt.Sprintf("/api/admin/jobs/%d", j.ID), &j)
	}
	if j.Status != jobs.Done || j.Progress.Done != len(file) {
		t.Fatalf("task = %+v", j)
	}
	get("/api/admin/downloads", &list)
	if l := list[0]; l.Loaded == nil || l.Loaded.Type != "DBIP-City-Lite" || l.Size == nil || *l.Size != int64(len(file)) {
		t.Errorf("after = %+v", l)
	}
	get("/api/about", &about)
	if about.GeoIP == nil || about.GeoIP.Provider != "db-ip" {
		t.Errorf("about after = %+v", about.GeoIP)
	}
}
