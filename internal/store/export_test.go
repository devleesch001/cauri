package store

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"
)

// The keyset of EachSession and EachLoginAttempt must give every row once,
// in order, across batches, including rows with the same time.
func TestEachAcrossBatches(t *testing.T) {
	old := exportBatch
	exportBatch = 2
	defer func() { exportBatch = old }()

	db := testDB(t)
	ctx := context.Background()
	sn := Sensor{Name: "hp"}
	if _, err := db.NewInsert().Model(&sn).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	for i, at := range []time.Duration{0, time.Second, time.Second, time.Second, 2 * time.Second} {
		s := Session{SensorID: sn.ID, CowrieSession: string(rune('a' + i)), StartedAt: NewTime(t0.Add(at))}
		if _, err := db.NewInsert().Model(&s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		la := LoginAttempt{SessionID: s.ID, TS: NewTime(t0.Add(at)), Username: "u", Password: "p"}
		if _, err := db.NewInsert().Model(&la).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	err := EachSession(ctx, db, SessionFilter{}, 100, func(rows []SessionSummary) error {
		for _, r := range rows {
			got = append(got, r.CowrieSession)
		}
		return nil
	})
	if err != nil || len(got) != 5 || got[0] != "e" || got[4] != "a" || got[1] != "d" || got[3] != "b" {
		t.Errorf("sessions = %v, %v", got, err)
	}
	got = nil
	_ = EachSession(ctx, db, SessionFilter{}, 3, func(rows []SessionSummary) error {
		for _, r := range rows {
			got = append(got, r.CowrieSession)
		}
		return nil
	})
	if len(got) != 3 {
		t.Errorf("limited to 3: %v", got)
	}
	n := 0
	err = EachLoginAttempt(ctx, db, LoginAttemptFilter{}, 100, func(rows []LoginAttemptRow) error {
		n += len(rows)
		return nil
	})
	if err != nil || n != 5 {
		t.Errorf("login attempts = %d, %v", n, err)
	}
}

func testDB(t *testing.T) *bun.DB {
	t.Helper()
	ctx := context.Background()
	db, err := Open(ctx, SQLite, t.TempDir()+"/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	return db
}
