package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"cauri/internal/store"
	"cauri/internal/store/storetest"
)

func TestMigrateIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		if err := store.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	})
}

// TestTimeRoundTrip checks that times survive the database unchanged and
// sort chronologically, which SQLite does by comparing text.
func TestTimeRoundTrip(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		base := time.Date(2026, 10, 14, 9, 0, 5, 0, time.UTC)
		times := []time.Time{
			base.Add(500 * time.Millisecond), // .5
			base,                             // no fraction
			base.Add(123456 * time.Microsecond),
		}
		for i, ts := range times {
			s := &store.Sensor{ID: string(rune('a' + i)), FirstSeen: ts, LastSeen: ts}
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
			if s.ID != want[i] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
		if !got[2].FirstSeen.Equal(times[0]) {
			t.Errorf("time = %v, want %v", got[2].FirstSeen, times[0])
		}
		n, err := db.NewSelect().Model((*store.Sensor)(nil)).Where("first_seen > ?", base).Count(ctx)
		if err != nil || n != 2 {
			t.Errorf("count after base = %d, %v", n, err)
		}
	})
}

func TestSQLiteForeignKeys(t *testing.T) {
	db := storetest.SQLite(t)
	_, err := db.NewInsert().Model(&store.Session{
		ID: "x", SensorID: "unknown", Session: "s", StartedAt: time.Now().UTC(),
	}).Exec(context.Background())
	if err == nil {
		t.Fatal("want foreign key violation")
	}
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
