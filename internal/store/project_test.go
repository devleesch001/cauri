package store_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// Migration 00009 projects the raw lines stored before it: from the schema
// of 00008 (its down migration, after 00013's: the raw lines had their
// first name then), with a session whose key exchange line is there, one
// known only by its HASSH, and lines with fields no table holds.
func TestProjectRawEvents(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		inTx := func(fn func(context.Context, bun.Tx) error) {
			t.Helper()
			if err := db.RunInTx(ctx, nil, fn); err != nil {
				t.Fatal(err)
			}
		}
		runMigration(t, db, "00013_event_raw_data.tx.down.sql")
		inTx(store.ProjectDown)

		sn := store.Sensor{Name: "hp"}
		if _, err := db.NewInsert().Model(&sn).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
		session := func(cs string) int64 {
			s := store.Session{SensorID: sn.ID, CowrieSession: cs, StartedAt: store.NewTime(t0)}
			if _, err := db.NewInsert().Model(&s).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			return s.ID
		}
		a, b := session("a"), session("b")
		if _, err := db.ExecContext(ctx, "UPDATE sessions SET hassh = ? WHERE id = ?", "h0", b); err != nil {
			t.Fatal(err)
		}
		n := 0
		event := func(sid int64, typ, raw string) int64 {
			n++
			e := store.Event{Hash: []byte{byte(n)}, SensorID: sn.ID, SessionID: &sid, Type: typ,
				TS: store.NewTime(t0.Add(time.Duration(n) * time.Second)), ReceivedAt: store.NewTime(t0)}
			if _, err := db.NewInsert().Model(&e).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO event_data (event_id, data) VALUES (?, ?)", e.ID, raw); err != nil {
				t.Fatal(err)
			}
			return e.ID
		}
		kex := `{"eventid":"cowrie.client.kex","session":"a","hassh":"h1","kexAlgs":["k"],"encCS":["e"],"macCS":["m"],"compCS":["none"],"langCS":[""],"keyAlgs":`
		connect := event(a, "cowrie.session.connect", `{"eventid":"cowrie.session.connect","session":"a","src_ip":"203.0.113.7","message":"New connection"}`)
		event(a, "cowrie.client.kex", kex+`["ssh-ed25519"]}`)
		event(a, "cowrie.client.kex", kex+`["ssh-rsa"]}`) // a rekey: the first one counts
		v := event(a, "cowrie.client.var", `{"eventid":"cowrie.client.var","session":"a","name":"LANG","value":"C","message":"request_env: LANG=C"}`)

		inTx(store.ProjectUp)
		runMigration(t, db, "00013_event_raw_data.tx.up.sql")

		var kexes []store.SSHKex
		if err := db.NewSelect().Model(&kexes).OrderExpr("hassh").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(kexes) != 3 || *kexes[0].HASSH != "h0" || kexes[0].KexAlgs != nil ||
			*kexes[1].HASSH != "h1" || *kexes[2].HASSH != "h1" {
			t.Fatalf("ssh_kex = %+v", kexes)
		}
		for sid, want := range map[int64]string{a: "ssh-ed25519", b: ""} {
			s, err := store.GetSession(ctx, db, sid)
			if err != nil {
				t.Fatal(err)
			}
			var k store.SSHKex
			if err := db.NewSelect().Model(&k).Where("id = ?", *s.SSHKexID).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			if text(k.KeyAlgs) != want {
				t.Errorf("session %d: key algorithms %v, want %q", sid, k.KeyAlgs, want)
			}
		}
		for id, want := range map[int64]string{connect: "", v: `{"name":"LANG","value":"C"}`} {
			var data *string
			if err := db.NewSelect().Model((*store.Event)(nil)).Column("data").Where("id = ?", id).Scan(ctx, &data); err != nil {
				t.Fatal(err)
			}
			if text(data) != want {
				t.Errorf("event %d: data %v, want %q", id, data, want)
			}
		}
		// Qualified: SQLite reads an unknown quoted name as a string.
		var hassh []string
		if err := db.NewSelect().TableExpr("sessions AS s").ColumnExpr("s.hassh").Scan(ctx, &hassh); err == nil {
			t.Error("sessions.hassh is still there")
		}
	})
}

func text(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// runMigration runs one SQL migration file of the engine of db, in a
// transaction.
func runMigration(t *testing.T, db *bun.DB, file string) {
	t.Helper()
	engine := store.SQLite
	if db.Dialect().Name().String() == "pg" {
		engine = store.Postgres
	}
	qs, err := store.MigrationSQL(engine, file)
	if err != nil {
		t.Fatal(err)
	}
	err = db.RunInTx(context.Background(), nil, func(ctx context.Context, tx bun.Tx) error {
		for _, q := range qs {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
