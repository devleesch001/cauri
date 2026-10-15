package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

func TestPurgeRawEvents(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		s1 := lookup(t, db, "hp-01", "1")
		// A terminal size of session 1, which replays need.
		ev := store.Event{Hash: []byte("size-of-session-1-hash-32-bytes!"), SensorID: 1, SessionID: &s1,
			Type: "cowrie.client.size", TS: store.NewTime(t0.Add(time.Second)), ReceivedAt: store.NewTime(t0)}
		if _, err := db.NewInsert().Model(&ev).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewInsert().Model(&store.EventData{EventID: ev.ID, Data: `{"width":80,"height":24}`}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		count := func(model any) int {
			n, err := db.NewSelect().Model(model).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return int(n)
		}
		events, logins := count((*store.Event)(nil)), count((*store.LoginAttempt)(nil))

		// Sessions 1 (8 events) and 2 (2 events) are before t0+90min.
		n, err := store.PurgeRawEvents(ctx, db, t0.Add(90*time.Minute))
		if err != nil || n != 10 {
			t.Fatalf("purged %d, %v", n, err)
		}
		if count((*store.Event)(nil)) != events || count((*store.LoginAttempt)(nil)) != logins {
			t.Error("a purge must keep events and projections")
		}
		d, err := store.GetSession(ctx, db, s1)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range d.Events {
			kept := e.EventID == "cowrie.client.size"
			if (e.Data != nil) != kept {
				t.Errorf("%s: data %s", e.EventID, e.Data)
			}
		}
		if n, err := store.PurgeRawEvents(ctx, db, t0.Add(90*time.Minute)); n != 0 || err != nil {
			t.Errorf("second purge: %d, %v", n, err)
		}
	})
}
