package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

type ev map[string]any

// seed ingests three sessions:
//
//	A  hp-01  t0      root/admin fails, root/123456 succeeds, 2 commands, tty stored
//	B  hp-01  t0+1h   admin/admin fails
//	C  hp-02  t0+2h   test/test fails, client libssh_0.9.6
func seed(t *testing.T, db *bun.DB) *ingest.Service {
	t.Helper()
	tty, err := ttystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := ingest.New(db, tty, 0)
	ctx := context.Background()
	ttyContent := "recording"
	ttySum := sha256.Sum256([]byte(ttyContent))
	ttySHA := hex.EncodeToString(ttySum[:])

	send := func(sensor, sess string, start time.Duration, events ...ev) {
		b := protocol.EventBatch{SensorID: sensor}
		for i, e := range events {
			e["session"] = sess
			e["src_ip"] = "203.0.113." + sess
			e["timestamp"] = t0.Add(start + time.Duration(i)*time.Second).Format(time.RFC3339Nano)
			raw, _ := json.Marshal(e)
			b.Events = append(b.Events, protocol.Event{ID: protocol.EventID(sensor, raw), Raw: raw})
		}
		if _, err := svc.IngestEvents(ctx, sensor, b); err != nil {
			t.Fatal(err)
		}
	}
	send("hp-01", "1", 0,
		ev{"eventid": "cowrie.session.connect", "src_port": 1},
		ev{"eventid": "cowrie.client.version", "version": "SSH-2.0-OpenSSH_9.6"},
		ev{"eventid": "cowrie.login.failed", "username": "root", "password": "admin"},
		ev{"eventid": "cowrie.login.success", "username": "root", "password": "123456"},
		ev{"eventid": "cowrie.command.input", "input": "uname -a"},
		ev{"eventid": "cowrie.command.input", "input": "cat /proc/cpuinfo"},
		ev{"eventid": "cowrie.log.closed", "shasum": ttySHA, "size": len(ttyContent)},
		ev{"eventid": "cowrie.session.closed", "duration": 7.0},
	)
	send("hp-01", "2", time.Hour,
		ev{"eventid": "cowrie.session.connect"},
		ev{"eventid": "cowrie.login.failed", "username": "admin", "password": "admin"},
	)
	send("hp-02", "3", 2*time.Hour,
		ev{"eventid": "cowrie.session.connect"},
		ev{"eventid": "cowrie.client.version", "version": "SSH-2.0-libssh_0.9.6"},
		ev{"eventid": "cowrie.login.failed", "username": "test", "password": "test"},
	)
	m := protocol.TTYMeta{SensorID: "hp-01", Session: "1", SHA256: ttySHA, Size: int64(len(ttyContent))}
	if _, err := svc.StoreTTY(ctx, "hp-01", m, strings.NewReader(ttyContent)); err != nil {
		t.Fatal(err)
	}
	return svc
}

func ptr[T any](v T) *T { return &v }

func lookup(t *testing.T, db *bun.DB, sensor, cowrieSession string) int64 {
	t.Helper()
	id, err := store.LookupSession(context.Background(), db, sensor, cowrieSession)
	if err != nil {
		t.Fatalf("session %s/%s: %v", sensor, cowrieSession, err)
	}
	return id
}

func sessions(t *testing.T, db *bun.DB, f store.SessionFilter) ([]store.SessionSummary, string) {
	t.Helper()
	rows, total, err := store.ListSessions(context.Background(), db, f)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r.CowrieSession)
	}
	if f.Limit == 0 && total != len(rows) {
		t.Errorf("total = %d, rows = %d", total, len(rows))
	}
	return rows, b.String()
}

func TestListSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		cases := map[string]struct {
			f    store.SessionFilter
			want string // Cowrie session ids, in order
		}{
			"all, latest first": {store.SessionFilter{}, "321"},
			"sensor":            {store.SessionFilter{Sensor: "hp-01"}, "21"},
			"ip":                {store.SessionFilter{SrcIP: "203.0.113.2"}, "2"},
			"login success":     {store.SessionFilter{Login: store.LoginSuccess}, "1"},
			"login failed":      {store.SessionFilter{Login: store.LoginFailed}, "32"},
			"username":          {store.SessionFilter{Username: "root"}, "1"},
			"username+password": {store.SessionFilter{Username: "admin", Password: "admin"}, "2"},
			"password only":     {store.SessionFilter{Password: "admin"}, "21"},
			"client, any case":  {store.SessionFilter{Client: "LIBSSH"}, "3"},
			"client, wildcard":  {store.SessionFilter{Client: "%"}, ""},
			"has commands":      {store.SessionFilter{HasCommands: ptr(true)}, "1"},
			"no commands":       {store.SessionFilter{HasCommands: ptr(false)}, "32"},
			"has tty":           {store.SessionFilter{HasTTY: ptr(true)}, "1"},
			"from":              {store.SessionFilter{From: ptr(t0.Add(time.Hour))}, "32"},
			"to":                {store.SessionFilter{To: ptr(t0.Add(time.Hour))}, "1"},
			// 1 is uncertain (its recording is not one); 2 and 3 have no
			// interaction: all counted as bots.
			"verdict bot":   {store.SessionFilter{Verdict: store.VerdictBot}, "321"},
			"verdict human": {store.SessionFilter{Verdict: store.VerdictHuman}, ""},
		}
		for name, c := range cases {
			if _, got := sessions(t, db, c.f); got != c.want {
				t.Errorf("%s: got %q, want %q", name, got, c.want)
			}
		}
	})
}

func TestListSessionsPageAndSummary(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		rows, total, err := store.ListSessions(context.Background(), db, store.SessionFilter{Page: store.Page{Limit: 1, Offset: 2}})
		if err != nil || total != 3 || len(rows) != 1 {
			t.Fatalf("page: %d rows, total %d, %v", len(rows), total, err)
		}
		s := rows[0]
		if s.CowrieSession != "1" || s.LoginAttempts != 2 || s.Commands != 2 || !s.TTYAvailable || !s.LoginSuccess {
			t.Fatalf("summary = %+v", s)
		}
		if len(s.Credentials) != 2 || s.Credentials[1] != (store.Credential{Username: "root", Password: "123456", Success: true}) {
			t.Errorf("credentials = %+v", s.Credentials)
		}
		if *s.ClientVersion != "SSH-2.0-OpenSSH_9.6" || *s.DurationMS != 7000 || s.Sensor != "hp-01" || *s.TTYSize != 9 {
			t.Errorf("fields = %+v", s.Session)
		}
	})
}

func TestGetSession(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		d, err := store.GetSession(ctx, db, lookup(t, db, "hp-01", "1"))
		if err != nil {
			t.Fatal(err)
		}
		if len(d.Events) != 8 || d.Events[0].EventID != "cowrie.session.connect" || d.Events[7].EventID != "cowrie.session.closed" {
			t.Errorf("events = %+v", d.Events)
		}
		var raw map[string]any
		if err := json.Unmarshal(d.Events[0].Data, &raw); err != nil || raw["src_port"] != float64(1) {
			t.Errorf("raw data = %s, %v", d.Events[0].Data, err)
		}
		if len(d.CommandList) != 2 || d.CommandList[0].Input != "uname -a" || len(d.LoginAttemptList) != 2 {
			t.Errorf("lists = %+v / %+v", d.CommandList, d.LoginAttemptList)
		}
		if _, err := store.GetSession(ctx, db, 999); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("missing: err = %v", err)
		}
	})
}

func TestListLoginAttempts(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		get := func(f store.LoginAttemptFilter) string {
			rows, total, err := store.ListLoginAttempts(ctx, db, f)
			if err != nil {
				t.Fatal(err)
			}
			if total != len(rows) {
				t.Errorf("total = %d, rows = %d", total, len(rows))
			}
			var b strings.Builder
			for _, r := range rows {
				b.WriteString(r.Username + "/" + r.Password + "@" + r.Sensor + " ")
			}
			return b.String()
		}
		if got := get(store.LoginAttemptFilter{Success: ptr(false), Interactive: ptr(false)}); got != "test/test@hp-02 admin/admin@hp-01 " {
			t.Errorf("failed, no shell: %q", got)
		}
		if got := get(store.LoginAttemptFilter{Sensor: "hp-01", Username: "root"}); got != "root/123456@hp-01 root/admin@hp-01 " {
			t.Errorf("root on hp-01: %q", got)
		}
		if got := get(store.LoginAttemptFilter{Interactive: ptr(true), Success: ptr(false)}); got != "root/admin@hp-01 " {
			t.Errorf("failed before a shell: %q", got)
		}
	})
}

func TestListSensors(t *testing.T) {
	db := storetest.SQLite(t)
	seed(t, db)
	sensors, err := store.ListSensors(context.Background(), db)
	if err != nil || len(sensors) != 2 || sensors[0].Name != "hp-01" {
		t.Fatalf("sensors = %+v, %v", sensors, err)
	}
}

func TestCountries(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		// Locate the seeded sessions by hand: 1 and 3 in Iran, 2 in France.
		for sess, c := range map[string]string{"1": "IR", "2": "FR", "3": "IR"} {
			sensor := "hp-01"
			if sess == "3" {
				sensor = "hp-02"
			}
			if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("geo_country = ?", c).
				Where("id = ?", lookup(t, db, sensor, sess)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if _, got := sessions(t, db, store.SessionFilter{Country: "IR"}); got != "31" {
			t.Errorf("sessions from IR = %q", got)
		}
		// Session 1 (IR) obtained a shell.
		countries, err := store.ListCountries(ctx, db, store.StatsFilter{})
		if err != nil || len(countries) != 2 || countries[0] != (store.CountryCount{Country: "IR", Sessions: 2, Shells: 1}) ||
			countries[1] != (store.CountryCount{Country: "FR", Sessions: 1}) {
			t.Errorf("countries = %+v, %v", countries, err)
		}
		if c, _ := store.ListCountries(ctx, db, store.StatsFilter{Sensor: "hp-02"}); len(c) != 1 || c[0].Country != "IR" {
			t.Errorf("countries of hp-02 = %+v", c)
		}
		if c, _ := store.ListCountries(ctx, db, store.StatsFilter{From: ptr(t0.Add(time.Hour))}); len(c) != 2 || c[0].Sessions != 1 {
			t.Errorf("countries from t0+1h = %+v", c)
		}
		// 1 is uncertain, 2 and 3 without interaction: all bots.
		if c, _ := store.ListCountries(ctx, db, store.StatsFilter{Verdict: store.VerdictHuman}); len(c) != 0 {
			t.Errorf("countries of humans = %+v", c)
		}
		// Cities: 1 and 3 in Tehran, at positions of two database dates; 2 in
		// France without city nor coordinates (a country database).
		for sess, c := range map[string][3]any{"1": {"Tehran", 35.6, 51.4}, "3": {"Tehran", 35.8, 51.6}} {
			sensor := "hp-01"
			if sess == "3" {
				sensor = "hp-02"
			}
			if _, err := db.NewUpdate().Model((*store.Session)(nil)).
				Set("geo_city = ?", c[0]).Set("geo_lat = ?", c[1]).Set("geo_lon = ?", c[2]).
				Where("id = ?", lookup(t, db, sensor, sess)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		cities, err := store.ListCities(ctx, db, store.StatsFilter{})
		if err != nil || len(cities) != 1 {
			t.Fatalf("cities = %+v, %v", cities, err)
		}
		if c := cities[0]; c.Country != "IR" || c.City != "Tehran" || c.Sessions != 2 || c.Shells != 1 ||
			math.Abs(c.Lat-35.7) > 1e-9 || math.Abs(c.Lon-51.5) > 1e-9 {
			t.Errorf("Tehran = %+v, want the average position", c)
		}
		if c, _ := store.ListCities(ctx, db, store.StatsFilter{Sensor: "hp-02"}); len(c) != 1 || c[0].Sessions != 1 {
			t.Errorf("cities of hp-02 = %+v", c)
		}
		if _, got := sessions(t, db, store.SessionFilter{Country: "IR", City: "Tehran"}); got != "31" {
			t.Errorf("sessions from Tehran = %q", got)
		}
		if _, got := sessions(t, db, store.SessionFilter{Country: "FR", City: "Tehran"}); got != "" {
			t.Errorf("sessions from Tehran, France = %q", got)
		}

		rows, _, err := store.ListLoginAttempts(ctx, db, store.LoginAttemptFilter{Country: "FR"})
		if err != nil || len(rows) != 1 || *rows[0].Country != "FR" {
			t.Errorf("attempts from FR = %+v, %v", rows, err)
		}
	})
}
