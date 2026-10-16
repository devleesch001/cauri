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
		// A terminal size of session 1, which replays need: its data stays.
		size := `{"width":80,"height":24}`
		ev := store.Event{Hash: []byte("size-of-session-1-hash-32-bytes!"), SensorID: 1, SessionID: &s1,
			Type: "cowrie.client.size", TS: store.NewTime(t0.Add(time.Second)), ReceivedAt: store.NewTime(t0), Data: &size}
		if _, err := db.NewInsert().Model(&ev).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewInsert().Model(&store.EventData{EventID: ev.ID, Data: `{"eventid":"cowrie.client.size","width":80,"height":24}`}).Exec(ctx); err != nil {
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

		// Sessions 1 (8 events and the size) and 2 (2 events) are before t0+90min.
		n, err := store.PurgeRawEvents(ctx, db, t0.Add(90*time.Minute))
		if err != nil || n != 11 {
			t.Fatalf("purged %d, %v", n, err)
		}
		if count((*store.Event)(nil)) != events || count((*store.LoginAttempt)(nil)) != logins {
			t.Error("a purge must keep events and projections")
		}
		d, err := store.GetSession(ctx, db, s1)
		if err != nil {
			t.Fatal(err)
		}
		// Without the raw line, an event shows its data.
		for _, e := range d.Events {
			if e.EventID == "cowrie.client.size" && string(e.Data) != size {
				t.Errorf("size: data %s", e.Data)
			}
		}
		r, err := store.GetRecording(ctx, db, s1)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Sizes) != 1 || r.Sizes[0].Cols != 80 {
			t.Errorf("recording sizes = %+v", r.Sizes)
		}
		if n, err := store.PurgeRawEvents(ctx, db, t0.Add(90*time.Minute)); n != 0 || err != nil {
			t.Errorf("second purge: %d, %v", n, err)
		}
	})
}
