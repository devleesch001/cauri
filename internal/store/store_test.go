package store_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// Migrate says what the database is, what is pending, and each migration it
// runs; a current schema is said to be current; a schema ahead of this
// version is refused.
func TestMigrateLogs(t *testing.T) {
	ctx := context.Background()
	for _, engine := range []string{store.SQLite, store.Postgres} {
		t.Run(engine, func(t *testing.T) {
			var db *bun.DB
			if engine == store.SQLite {
				var err error
				db, err = store.Open(ctx, store.SQLite, filepath.Join(t.TempDir(), "fresh.db"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = db.Close() })
			} else {
				db = storetest.PostgresUnmigrated(t)
			}
			var out strings.Builder
			log := slog.New(slog.NewJSONHandler(&out, nil))
			if err := store.Migrate(ctx, db, log); err != nil {
				t.Fatal(err)
			}
			first := out.String()
			for _, want := range []string{
				`"msg":"database"`, `"engine":"` + engine + `"`, `"version":"`,
				`"msg":"database schema"`, `"version":"none (empty database)"`,
				`"msg":"database upgrade needed"`, `"msg":"applying migration","migration":"00001_init"`,
				`"msg":"migration applied","migration":"00012_networks"`, `"msg":"database upgraded"`,
			} {
				if !strings.Contains(first, want) {
					t.Errorf("first run: no %s in\n%s", want, first)
				}
			}
			if engine == store.SQLite && !strings.Contains(first, `"foreign_keys":true`) {
				t.Errorf("SQLite foreign keys not reported:\n%s", first)
			}

			out.Reset()
			if err := store.Migrate(ctx, db, log); err != nil {
				t.Fatal(err)
			}
			if second := out.String(); !strings.Contains(second, `"msg":"database schema is up to date"`) ||
				!strings.Contains(second, `"version":"00012_networks","latest":"00012_networks","pending":0`) ||
				strings.Contains(second, "applying migration") {
				t.Errorf("second run:\n%s", second)
			}

			// A migration this version does not know: the database is ahead.
			if _, err := db.ExecContext(ctx, "INSERT INTO bun_migrations (name, group_id, migrated_at) VALUES ('99999', 9, ?)", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := store.Migrate(ctx, db, log); !errors.Is(err, store.ErrNewerSchema) {
				t.Errorf("newer schema: err = %v", err)
			}
		})
	}
}

func TestMigrateIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		if err := store.Migrate(context.Background(), db, nil); err != nil {
			t.Fatal(err)
		}
	})
}

// A database of cauri v0.0.x is refused, not migrated beside its tables.
func TestMigrateRefusesOldSchema(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.SQLite, filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE TABLE goose_db_version (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db, nil); !errors.Is(err, store.ErrOldSchema) {
		t.Fatalf("err = %v, want ErrOldSchema", err)
	}
}

// TestTimeRoundTrip checks that times survive the database to the
// microsecond and sort chronologically.
func TestTimeRoundTrip(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		base := time.Date(2026, 10, 14, 9, 0, 5, 0, time.UTC)
		times := []time.Time{
			base.Add(500 * time.Millisecond),
			base,
			base.Add(123456 * time.Microsecond),
		}
		for i, ts := range times {
			st := store.NewTime(ts)
			s := &store.Sensor{Name: string(rune('a' + i)), FirstSeen: &st, LastSeen: &st}
			if _, err := db.NewInsert().Model(s).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var got []store.Sensor
		if err := db.NewSelect().Model(&got).Order("first_seen").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		want := []string{"b", "c", "a"}
		for i, s := range got {
			if s.Name != want[i] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
		if !got[1].FirstSeen.Equal(times[2]) {
			t.Errorf("time = %v, want %v", got[1].FirstSeen, times[2])
		}
		n, err := db.NewSelect().Model((*store.Sensor)(nil)).Where("first_seen > ?", store.NewTime(base)).Count(ctx)
		if err != nil || n != 2 {
			t.Errorf("count after base = %d, %v", n, err)
		}
		var empty store.Sensor
		if err := db.NewSelect().Model(&empty).Where("name = 'a'").Scan(ctx); err != nil || empty.FirstSeen == nil {
			t.Errorf("scan = %+v, %v", empty, err)
		}
		if _, err := db.NewInsert().Model(&store.Sensor{Name: "never"}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var never store.Sensor
		if err := db.NewSelect().Model(&never).Where("name = 'never'").Scan(ctx); err != nil || never.FirstSeen != nil {
			t.Errorf("null time = %+v, %v", never.FirstSeen, err)
		}
	})
}

func TestForeignKeys(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		_, err := db.NewInsert().Model(&store.Session{
			SensorID: 42, CowrieSession: "s", StartedAt: store.NewTime(time.Now()),
		}).Exec(context.Background())
		if err == nil {
			t.Fatal("want foreign key violation")
		}
	})
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := store.Open(ctx, "mysql", "x"); err == nil {
		t.Error("want error for unknown driver")
	}
	if _, err := store.Open(ctx, store.SQLite, ""); err == nil {
		t.Error("want error for empty dsn")
	}
}

// The two migration sets must describe the same tables, columns, nullness
// and indexes; only the types differ.
func TestSchemasMatch(t *testing.T) {
	if os.Getenv(storetest.EnvPostgres) == "" {
		t.Skip(storetest.EnvPostgres + " not set")
	}
	ctx := context.Background()
	lite := schema(t, ctx, storetest.SQLite(t), store.SQLite)
	pg := schema(t, ctx, storetest.Postgres(t), store.Postgres)
	if len(lite) < 80 {
		t.Fatalf("schema read back has %d lines: the queries miss something", len(lite))
	}
	if !slices.Equal(lite, pg) {
		for _, l := range lite {
			if !slices.Contains(pg, l) {
				t.Errorf("only in SQLite: %s", l)
			}
		}
		for _, l := range pg {
			if !slices.Contains(lite, l) {
				t.Errorf("only in PostgreSQL: %s", l)
			}
		}
	}
}

// schema lists "table.column null|not null", "pk name: table(column)",
// "index name: table(columns) [unique]" and "fk name: table(column) ->
// table(column) action" lines, sorted, without bun's own tables.
func schema(t *testing.T, ctx context.Context, db *bun.DB, engine string) []string {
	t.Helper()
	var lines []string
	add := func(q string) {
		var rows []string
		if err := db.NewRaw(q).Scan(ctx, &rows); err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		for _, r := range rows {
			if !strings.HasPrefix(r, "bun_") && !strings.Contains(r, ": bun_") {
				lines = append(lines, r)
			}
		}
	}
	if engine == store.SQLite {
		add(`SELECT m.name || '.' || c.name || CASE WHEN c."notnull" = 1 OR c.pk = 1 THEN ' not null' ELSE ' null' END
			FROM sqlite_master AS m, pragma_table_info(m.name) AS c
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
		// Named indexes, and the unique constraints SQLite indexes itself.
		add(`SELECT 'index ' || il.name || ': ' || m.name || '(' || (SELECT group_concat(name, ',') FROM (SELECT ii.name FROM pragma_index_info(il.name) AS ii ORDER BY ii.seqno)) || ')' ||
				CASE WHEN il."unique" = 1 THEN ' unique' ELSE '' END
			FROM sqlite_master AS m, pragma_index_list(m.name) AS il
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND il.origin != 'pk'`)
		// SQLite does not tell the name of a constraint: read it from the
		// definition of the table.
		var defs []struct{ Name, SQL string }
		if err := db.NewRaw(`SELECT name, sql AS "sql" FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'bun_%'`).Scan(ctx, &defs); err != nil {
			t.Fatal(err)
		}
		fk := regexp.MustCompile(`CONSTRAINT (\w+) FOREIGN KEY \((\w+)\) REFERENCES "?(\w+)"? \((\w+)\)(?: ON DELETE (CASCADE|RESTRICT|SET NULL))?(?: ON UPDATE (CASCADE|RESTRICT|SET NULL))?`)
		// A column added to a table holds its key in its own definition.
		column := regexp.MustCompile(`(\w+) \w+ CONSTRAINT (\w+) REFERENCES "?(\w+)"? \((\w+)\)(?: ON DELETE (CASCADE|RESTRICT|SET NULL))?(?: ON UPDATE (CASCADE|RESTRICT|SET NULL))?`)
		unnamed := regexp.MustCompile(`REFERENCES `)
		pk := regexp.MustCompile(`CONSTRAINT (\w+) PRIMARY KEY \((\w+)\)`)
		for _, d := range defs {
			keys := pk.FindAllStringSubmatch(d.SQL, -1)
			for _, m := range keys {
				lines = append(lines, fmt.Sprintf("pk %s: %s(%s)", m[1], d.Name, m[2]))
			}
			if n := strings.Count(d.SQL, "PRIMARY KEY"); n != len(keys) {
				t.Errorf("%s: %d primary keys, %d named", d.Name, n, len(keys))
			}
			named := fk.FindAllStringSubmatch(d.SQL, -1)
			for _, m := range column.FindAllStringSubmatch(d.SQL, -1) {
				named = append(named, []string{m[0], m[2], m[1], m[3], m[4], m[5], m[6]})
			}
			for _, m := range named {
				action := func(a string) string {
					if a == "" {
						return "no action"
					}
					return strings.ToLower(a)
				}
				lines = append(lines, fmt.Sprintf("fk %s: %s(%s) -> %s(%s) %s, update %s", m[1], d.Name, m[2], m[3], m[4], action(m[5]), action(m[6])))
			}
			if n := len(unnamed.FindAllString(d.SQL, -1)); n != len(named) {
				t.Errorf("%s: %d foreign keys, %d named", d.Name, n, len(named))
			}
		}
	} else {
		add(`SELECT table_name || '.' || column_name || CASE WHEN is_nullable = 'NO' THEN ' not null' ELSE ' null' END
			FROM information_schema.columns WHERE table_schema = current_schema()`)
		add(`SELECT 'index ' || i.relname || ': ' || t.relname || '(' || string_agg(a.attname, ',' ORDER BY k.n) || ')' ||
				CASE WHEN ix.indisunique THEN ' unique' ELSE '' END
			FROM pg_index AS ix
			JOIN pg_class AS i ON i.oid = ix.indexrelid
			JOIN pg_class AS t ON t.oid = ix.indrelid
			JOIN pg_namespace AS ns ON ns.oid = t.relnamespace
			CROSS JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, n)
			JOIN pg_attribute AS a ON a.attrelid = t.oid AND a.attnum = k.attnum
			WHERE ns.nspname = current_schema() AND NOT ix.indisprimary
			GROUP BY i.relname, t.relname, ix.indexrelid, ix.indisunique`)
		add(`SELECT 'pk ' || c.conname || ': ' || t.relname || '(' || a.attname || ')'
			FROM pg_constraint AS c
			JOIN pg_class AS t ON t.oid = c.conrelid
			JOIN pg_namespace AS ns ON ns.oid = t.relnamespace
			JOIN pg_attribute AS a ON a.attrelid = t.oid AND a.attnum = c.conkey[1]
			WHERE c.contype = 'p' AND ns.nspname = current_schema()`)
		add(`SELECT 'fk ' || c.conname || ': ' || t.relname || '(' || a.attname || ') -> ' || r.relname || '(' || ra.attname || ') ' ||
				CASE c.confdeltype WHEN 'c' THEN 'cascade' WHEN 'r' THEN 'restrict' WHEN 'n' THEN 'set null' WHEN 'd' THEN 'set default' ELSE 'no action' END ||
				', update ' ||
				CASE c.confupdtype WHEN 'c' THEN 'cascade' WHEN 'r' THEN 'restrict' WHEN 'n' THEN 'set null' WHEN 'd' THEN 'set default' ELSE 'no action' END
			FROM pg_constraint AS c
			JOIN pg_class AS t ON t.oid = c.conrelid
			JOIN pg_class AS r ON r.oid = c.confrelid
			JOIN pg_namespace AS ns ON ns.oid = t.relnamespace
			JOIN pg_attribute AS a ON a.attrelid = t.oid AND a.attnum = c.conkey[1]
			JOIN pg_attribute AS ra ON ra.attrelid = r.oid AND ra.attnum = c.confkey[1]
			WHERE c.contype = 'f' AND ns.nspname = current_schema()`)
	}
	slices.Sort(lines)
	return lines
}

// Every key and index is named after its table and columns (AGENTS.md),
// never by the engine: pk_<table>, uk_ or ix_<table>_<columns>,
// fk_<table>_<column>; every table has a primary key.
func TestKeyNames(t *testing.T) {
	line := regexp.MustCompile(`^(pk|index|fk) (\w+): (\w+)\(([\w,]+)\)( unique)?`)
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		engine := store.SQLite
		if db.Dialect().Name().String() == "pg" {
			engine = store.Postgres
		}
		tables, keyed := map[string]bool{}, map[string]bool{}
		for _, l := range schema(t, context.Background(), db, engine) {
			if table, _, ok := strings.Cut(l, "."); ok && !strings.Contains(table, " ") {
				tables[table] = true
				continue
			}
			m := line.FindStringSubmatch(l)
			if m == nil {
				t.Errorf("unread schema line: %s", l)
				continue
			}
			kind, name, table, cols := m[1], m[2], m[3], strings.ReplaceAll(m[4], ",", "_")
			want := "fk_" + table + "_" + cols
			switch {
			case kind == "pk":
				want = "pk_" + table
				keyed[table] = true
			case kind == "index" && m[5] != "":
				want = "uk_" + table + "_" + cols
			case kind == "index":
				want = "ix_" + table + "_" + cols
			}
			if name != want {
				t.Errorf("%s: named %s, want %s", l, name, want)
			}
		}
		for table := range tables {
			if !keyed[table] {
				t.Errorf("%s: no named primary key", table)
			}
		}
	})
}

// Every foreign key is named fk_<table>_<column>, has the action its
// parent's removal calls for (see docs/db.md) and follows a change of its
// parent's id, on both engines.
func TestForeignKeyNamesAndActions(t *testing.T) {
	want := []string{
		"fk fk_commands_session_id: commands(session_id) -> sessions(id) cascade, update cascade",
		"fk fk_event_data_event_id: event_data(event_id) -> events(id) cascade, update cascade",
		"fk fk_events_sensor_id: events(sensor_id) -> sensors(id) restrict, update cascade",
		"fk fk_events_session_id: events(session_id) -> sessions(id) cascade, update cascade",
		"fk fk_ingest_tokens_sensor_id: ingest_tokens(sensor_id) -> sensors(id) cascade, update cascade",
		"fk fk_login_attempts_session_id: login_attempts(session_id) -> sessions(id) cascade, update cascade",
		"fk fk_sessions_geo_source_id: sessions(geo_source_id) -> geo_sources(id) set null, update cascade",
		"fk fk_sessions_network_id: sessions(network_id) -> networks(id) set null, update cascade",
		"fk fk_sessions_sensor_id: sessions(sensor_id) -> sensors(id) restrict, update cascade",
		"fk fk_sessions_ssh_kex_id: sessions(ssh_kex_id) -> ssh_kex(id) restrict, update cascade",
		"fk fk_sessions_tty_file_id: sessions(tty_file_id) -> tty_files(id) set null, update cascade",
		"fk fk_transfers_session_id: transfers(session_id) -> sessions(id) cascade, update cascade",
	}
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		engine := store.SQLite
		if db.Dialect().Name().String() == "pg" {
			engine = store.Postgres
		}
		var got []string
		for _, l := range schema(t, context.Background(), db, engine) {
			if strings.HasPrefix(l, "fk ") {
				got = append(got, l)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("foreign keys:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// What removing a parent does, as the actions say: a session takes its
// events, their raw lines and its projections with it; a recording leaves
// its sessions without one; a sensor that has sessions cannot go, one with
// only tokens takes them along. A parent's id changed, its children follow.
func TestForeignKeyActions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := db.ExecContext(ctx, q, args...); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		count := func(table string) int {
			t.Helper()
			var n int
			if err := db.NewRaw("SELECT COUNT(*) FROM "+table).Scan(ctx, &n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		exec("INSERT INTO sensors (id, name) VALUES (1, 'hp-01'), (2, 'idle')")
		exec("INSERT INTO ingest_tokens (sensor_id, name, token_hash, created_at) VALUES (1, 'a', 'h1', 0), (2, 'b', 'h2', 0)")
		exec("INSERT INTO tty_files (id, sha256, size, path, stored_at) VALUES (1, 'sha', 1, 'p', 0)")
		exec("INSERT INTO sessions (id, sensor_id, cowrie_session, started_at, tty_file_id) VALUES (1, 1, 's1', 0, 1), (2, 1, 's2', 0, 1)")
		exec("INSERT INTO events (id, hash, sensor_id, session_id, type, ts, received_at) VALUES (1, ?, 1, 1, 'cowrie.session.connect', 0, 0), (2, ?, 1, 2, 'cowrie.session.connect', 0, 0)", []byte{1}, []byte{2})
		exec("INSERT INTO event_data (event_id, data) VALUES (1, '{}'), (2, '{}')")
		exec("INSERT INTO login_attempts (session_id, ts, username, password, success) VALUES (1, 0, 'root', 'x', ?)", false)
		exec("INSERT INTO commands (session_id, ts, input) VALUES (1, 0, 'id')")
		exec("INSERT INTO transfers (session_id, ts, direction) VALUES (1, 0, 'download')")

		// Ids changed: the references follow.
		exec("UPDATE sensors SET id = 10 WHERE id = 1")
		exec("UPDATE sessions SET id = 11 WHERE id = 1")
		exec("UPDATE sessions SET id = 1 WHERE id = 11")
		exec("UPDATE sensors SET id = 1 WHERE id = 10")
		for table, want := range map[string]int{"sessions": 2, "events": 2, "ingest_tokens": 2, "login_attempts": 1, "commands": 1, "transfers": 1} {
			if got := count(table); got != want {
				t.Errorf("%s = %d after changing ids, want %d", table, got, want)
			}
		}
		var moved int
		if err := db.NewRaw("SELECT COUNT(*) FROM events WHERE sensor_id = 1 AND session_id IN (1, 2)").Scan(ctx, &moved); err != nil || moved != 2 {
			t.Errorf("events following their sensor and session = %d, %v", moved, err)
		}
		exec("UPDATE sessions SET id = 21 WHERE id = 1")
		for _, table := range []string{"events", "login_attempts", "commands", "transfers"} {
			var n int
			if err := db.NewRaw("SELECT COUNT(*) FROM "+table+" WHERE session_id = 21").Scan(ctx, &n); err != nil || n != 1 {
				t.Errorf("%s of the renumbered session = %d, %v", table, n, err)
			}
		}
		exec("UPDATE sessions SET id = 1 WHERE id = 21")

		// A sensor with sessions or events stays.
		if _, err := db.ExecContext(ctx, "DELETE FROM sensors WHERE id = 1"); err == nil {
			t.Error("a sensor with sessions was deleted")
		}
		// One with only tokens goes with them.
		exec("DELETE FROM sensors WHERE id = 2")
		if n := count("ingest_tokens"); n != 1 {
			t.Errorf("tokens = %d after deleting their sensor, want 1", n)
		}

		// A recording removed: its sessions stay, without it.
		exec("DELETE FROM tty_files WHERE id = 1")
		var detached int
		if err := db.NewRaw("SELECT COUNT(*) FROM sessions WHERE tty_file_id IS NULL").Scan(ctx, &detached); err != nil || detached != 2 {
			t.Errorf("sessions without recording = %d, %v; want 2", detached, err)
		}

		// A session removed: everything of it.
		exec("DELETE FROM sessions WHERE id = 1")
		for table, want := range map[string]int{
			"sessions": 1, "events": 1, "event_data": 1, "login_attempts": 0, "commands": 0, "transfers": 0,
		} {
			if got := count(table); got != want {
				t.Errorf("%s = %d after deleting a session, want %d", table, got, want)
			}
		}
	})
}
