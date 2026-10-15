package store_test

import (
	"context"
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// Migration 00005 turns the recording of each session into its recordings,
// one per cowrie.log.closed still stored.
func TestMigrateSessionRecordings(t *testing.T) {
	storetest.RunEmpty(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		if err := store.MigrateTo(ctx, db, "00004"); err != nil {
			t.Fatal(err)
		}
		const t0 = int64(1792000000000000) // microseconds
		const s = int64(1_000_000)
		exec := func(q string, args ...any) {
			t.Helper()
			if _, err := db.NewRaw(q, args...).Exec(ctx); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		exec("INSERT INTO sensors (id, name) VALUES (1, 'hp-01')")
		exec("INSERT INTO tty_files (id, sha256, size, path, stored_at) VALUES (1, 'f1', 10, 'f1', 0)")
		// a: three exec channels, the third a duplicate of the first; the
		// API kept the last name and the file of the first channel.
		// b: its lines were purged. c: lines of unexpected types.
		exec(`INSERT INTO sessions (id, sensor_id, cowrie_session, started_at, ended_at, login_success, tty_log, tty_duration_ms, tty_file_id)
			VALUES (1, 1, 'a', ?, NULL, ?, 'uname', 100, 1), (2, 1, 'b', ?, ?, ?, 'b', 2000, 1), (3, 1, 'c', ?, NULL, ?, NULL, NULL, NULL)`,
			t0, false, t0, t0+10*s, false, t0, false)
		events := []struct {
			session int64
			ts      int64
			data    string
		}{
			{1, t0 + s, `{"ttylog":"var/lib/cowrie/tty/uname","duration_ms":300,"duplicate":false}`},
			{1, t0 + 2*s, `{"ttylog":"var/lib/cowrie/tty/who","duration_ms":200,"duplicate":false}`},
			{1, t0 + 3*s, `{"ttylog":"var/lib/cowrie/tty/uname","duration_ms":100,"duplicate":true}`},
			{3, t0 + s, `{"ttylog":5,"duration_ms":100}`},
			{3, t0 + 2*s, `{"ttylog":"/x/odd","duration_ms":"abc","duplicate":"yes"}`},
			{3, t0 + 4*s, `{"ttylog":"old","duration":1.5}`},
		}
		for i, e := range events {
			exec("INSERT INTO events (id, hash, sensor_id, session_id, type, ts, received_at) VALUES (?, ?, 1, ?, 'cowrie.log.closed', ?, 0)",
				i+1, []byte{byte(i)}, e.session, e.ts)
			exec("INSERT INTO event_data (event_id, data) VALUES (?, ?)", i+1, e.data)
		}

		if err := store.Migrate(ctx, db); err != nil {
			t.Fatal(err)
		}

		type rec struct {
			Session   int64  `bun:"session_id"`
			TTYLog    string `bun:"ttylog"`
			StartedAt int64  `bun:"started_at"`
			Duration  *int64 `bun:"duration_ms"`
			Duplicate bool   `bun:"duplicate"`
			File      *int64 `bun:"tty_file_id"`
		}
		var got []rec
		if err := db.NewRaw("SELECT session_id, ttylog, started_at, duration_ms, duplicate, tty_file_id FROM session_recordings ORDER BY session_id, started_at").
			Scan(ctx, &got); err != nil {
			t.Fatal(err)
		}
		ms := func(v int64) *int64 { return &v }
		one := ms(1)
		want := []rec{
			{1, "uname", t0 + s - 300_000, ms(300), false, one},
			{1, "who", t0 + 2*s - 200_000, ms(200), false, nil},
			{1, "uname", t0 + 3*s - 100_000, ms(100), true, one},
			{2, "b", t0 + 8*s, ms(2000), false, one},
			{3, "odd", t0 + 2*s, nil, false, nil},
			{3, "old", t0 + 4*s - 1_500_000, ms(1500), false, nil},
		}
		if len(got) != len(want) {
			t.Fatalf("recordings = %+v", got)
		}
		for i := range want {
			g, w := got[i], want[i]
			if g.Session != w.Session || g.TTYLog != w.TTYLog || g.StartedAt != w.StartedAt || g.Duplicate != w.Duplicate ||
				!eqPtr(g.Duration, w.Duration) || !eqPtr(g.File, w.File) {
				t.Errorf("#%d = %+v, want %+v", i, g, w)
			}
		}

		// Sessions keep their rows and what points to them.
		var n int
		if err := db.NewRaw("SELECT COUNT(*) FROM events AS e JOIN sessions AS s ON s.id = e.session_id").Scan(ctx, &n); err != nil || n != 6 {
			t.Errorf("events of sessions = %d, %v", n, err)
		}
		if db.Dialect().Name().String() != "pg" {
			var broken []string
			if err := db.NewRaw("SELECT \"table\" FROM pragma_foreign_key_check").Scan(ctx, &broken); err != nil || len(broken) != 0 {
				t.Errorf("foreign keys broken in %v, %v", broken, err)
			}
		}
	})
}

func eqPtr(a, b *int64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) }
