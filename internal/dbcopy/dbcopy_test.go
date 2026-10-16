package dbcopy_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/dbcopy"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

const sensor = "hp-01"

type locator struct{}

func (locator) Locate(string) (geoip.Location, geoip.Info, bool) {
	return geoip.Location{Country: "FR", City: "Lille", Lat: 50.63, Lon: 3.06, HasCoords: true},
		geoip.Info{Type: "DBIP-City-Lite", Built: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, true
}

func (locator) Network(string) (geoip.Network, bool) {
	return geoip.Network{ASN: 64500, Org: "Example"}, true
}

// seed fills db through the ingestion service with the real Cowrie samples
// and a download, so that every table has rows.
func seed(t *testing.T, db *bun.DB) {
	t.Helper()
	ctx := context.Background()
	tty, err := ttystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := ingest.New(db, tty, 0)
	svc.SetLocator(locator{})
	svc.SetNetworker(locator{})
	var lines [][]byte
	for _, f := range []string{"cowrie-v3.1.2.json", "cowrie-v3.1.2-exec.json", "cowrie-56b2abb-fragments.json"} {
		data, err := os.ReadFile(filepath.Join("..", "ingest", "testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, bytes.Split(bytes.TrimSpace(data), []byte("\n"))...)
	}
	lines = append(lines, []byte(`{"eventid":"cowrie.session.file_download","session":"6f07bbfeafaa","timestamp":"2026-10-14T09:00:00Z","url":"http://203.0.113.9/x.sh","shasum":"`+
		"aa"+string(bytes.Repeat([]byte("0"), 62))+`","outfile":"var/lib/cowrie/downloads/x"}`))
	b := protocol.EventBatch{SensorID: sensor}
	for _, l := range lines {
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID(sensor, l), Raw: l})
	}
	if _, err := svc.IngestEvents(ctx, sensor, b); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StoreTTY(ctx, sensor, protocol.TTYMeta{SensorID: sensor, Session: "6f07bbfeafaa",
		SHA256: "2c26b46b68ffc68ff99b453c1d30413413422d706483bfa0f98a5e886266e7ae", Size: 3}, bytes.NewReader([]byte("foo"))); err != nil {
		t.Fatal(err)
	}
	if _, _, err := auth.NewTokens(db).Create(ctx, sensor, "test"); err != nil {
		t.Fatal(err)
	}
	counts, err := dbcopy.Counts(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range counts {
		if c.Rows == 0 {
			t.Fatalf("seed leaves %s empty: the copy of its rows would go untested", c.Table)
		}
	}
}

// rows reads every row of every copied table, by model, in key order.
func rows(t *testing.T, db *bun.DB) map[string]any {
	t.Helper()
	ctx := context.Background()
	out := map[string]any{}
	read := func(name, key string, dst any) {
		if err := db.NewSelect().Model(dst).OrderExpr("? ASC", bun.Ident(key)).Scan(ctx); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = dst
	}
	read("sensors", "id", &[]store.Sensor{})
	read("ingest_tokens", "id", &[]store.IngestToken{})
	read("geo_sources", "id", &[]store.GeoSource{})
	read("networks", "id", &[]store.Network{})
	read("ssh_kex", "id", &[]store.SSHKex{})
	read("tty_files", "id", &[]store.TTYFile{})
	read("sessions", "id", &[]store.Session{})
	read("events", "id", &[]store.Event{})
	read("event_raw_data", "event_id", &[]store.EventRawData{})
	read("login_attempts", "id", &[]store.LoginAttempt{})
	read("commands", "id", &[]store.Command{})
	read("transfers", "id", &[]store.Transfer{})
	if len(out) != len(dbcopy.Tables()) {
		t.Fatalf("rows reads %d tables, the copy has %d", len(out), len(dbcopy.Tables()))
	}
	return out
}

// copyBetween copies a seeded database into an empty one and checks that
// both hold the same rows, then that the target takes new rows.
func copyBetween(t *testing.T, src, dst *bun.DB) {
	ctx := context.Background()
	seed(t, src)
	if _, err := dbcopy.Copy(ctx, src, dst, nil, nil); err != nil {
		t.Fatal(err)
	}
	want, got := rows(t, src), rows(t, dst)
	for name, w := range want {
		if !reflect.DeepEqual(w, got[name]) {
			t.Errorf("%s differs:\nsource %+v\ntarget %+v", name, w, got[name])
		}
	}
	// Ids go on after the copied ones.
	s := store.Sensor{Name: "hp-02"}
	if _, err := dst.NewInsert().Model(&s).Exec(ctx); err != nil {
		t.Fatalf("insert after the copy: %v", err)
	}
	// A second copy finds the target full.
	if _, err := dbcopy.Copy(ctx, src, dst, nil, nil); !errors.Is(err, dbcopy.ErrTargetNotEmpty) {
		t.Errorf("copy into a full database: %v", err)
	}
}

func unmigratedSQLite(t *testing.T) *bun.DB {
	t.Helper()
	db, err := store.Open(context.Background(), store.SQLite, filepath.Join(t.TempDir(), "target.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestCopy(t *testing.T) {
	t.Run("sqlite to sqlite", func(t *testing.T) { copyBetween(t, storetest.SQLite(t), unmigratedSQLite(t)) })
	// A target migrated but never used is empty too.
	t.Run("sqlite to migrated sqlite", func(t *testing.T) { copyBetween(t, storetest.SQLite(t), storetest.SQLite(t)) })
	if os.Getenv(storetest.EnvPostgres) == "" {
		return
	}
	t.Run("sqlite to postgres", func(t *testing.T) { copyBetween(t, storetest.SQLite(t), storetest.PostgresUnmigrated(t)) })
	t.Run("postgres to sqlite", func(t *testing.T) { copyBetween(t, storetest.Postgres(t), unmigratedSQLite(t)) })
}

func TestCheckRefusesAnOutdatedSource(t *testing.T) {
	ctx := context.Background()
	if _, err := dbcopy.Check(ctx, unmigratedSQLite(t), unmigratedSQLite(t)); !errors.Is(err, store.ErrSchemaNotCurrent) {
		t.Errorf("source without migrations: %v", err)
	}
}

// Every table of the schema is copied: a table added later cannot be
// forgotten.
func TestTablesCovered(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		q := "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'bun_%'"
		if db.Dialect().Name().String() == "pg" {
			q = "SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name NOT LIKE 'bun_%'"
		}
		var names []string
		if err := db.NewRaw(q).Scan(context.Background(), &names); err != nil {
			t.Fatal(err)
		}
		slices.Sort(names)
		copied := slices.Sorted(slices.Values(dbcopy.Tables()))
		if !slices.Equal(names, copied) {
			t.Errorf("schema tables %v, copied %v", names, copied)
		}
	})
}

// Every column of a copied table is a column of its model: the copy goes
// through the models, a column they miss would be lost.
func TestColumnsCovered(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		for name, model := range rows(t, db) {
			typ := reflect.TypeOf(model).Elem().Elem()
			var fields []string
			for _, f := range db.Table(typ).Fields {
				fields = append(fields, f.Name)
			}
			q := "SELECT name FROM pragma_table_info(?)"
			if db.Dialect().Name().String() == "pg" {
				q = "SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?"
			}
			var columns []string
			if err := db.NewRaw(q, name).Scan(context.Background(), &columns); err != nil {
				t.Fatal(err)
			}
			slices.Sort(fields)
			slices.Sort(columns)
			if !slices.Equal(fields, columns) {
				t.Errorf("%s: columns %v, model %v", name, columns, fields)
			}
		}
	})
}
