package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

func TestPurgeSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := seed(t, db)
		ctx := context.Background()
		sum := sha256.Sum256([]byte("recording"))
		sha := hex.EncodeToString(sum[:])
		// B shares A's recording: deleting A must keep it.
		m := protocol.TTYMeta{SensorID: "hp-01", Session: "2", SHA256: sha, Size: int64(len("recording"))}
		if _, err := svc.StoreTTY(ctx, "hp-01", m, strings.NewReader("recording")); err != nil {
			t.Fatal(err)
		}

		a := store.SessionFilter{SrcIP: "203.0.113.1"}
		want := store.PurgeCounts{Sessions: 1, Events: 8, LoginAttempts: 2, Commands: 2}
		if c, err := store.CountPurge(ctx, db, a); err != nil || c != want {
			t.Fatalf("count A = %+v, %v; want %+v", c, err, want)
		}
		c, shas, err := store.DeleteSessionBatch(ctx, db, a, 10)
		if err != nil || c != want || len(shas) != 0 {
			t.Fatalf("delete A = %+v %v, %v", c, shas, err)
		}
		if c, _, err := store.DeleteSessionBatch(ctx, db, a, 10); err != nil || c != (store.PurgeCounts{}) {
			t.Fatalf("delete A again = %+v, %v", c, err)
		}

		// B was the last session with the recording, C goes in the same
		// filter: two batches of one.
		bc := store.SessionFilter{Login: store.LoginFailed}
		if c, err := store.CountPurge(ctx, db, bc); err != nil || c.Sessions != 2 || c.Recordings != 1 {
			t.Fatalf("count B and C = %+v, %v", c, err)
		}
		var total store.PurgeCounts
		var removed []string
		for {
			c, shas, err := store.DeleteSessionBatch(ctx, db, bc, 1)
			if err != nil {
				t.Fatal(err)
			}
			if c.Sessions == 0 {
				break
			}
			total.Add(c)
			removed = append(removed, shas...)
		}
		if total.Sessions != 2 || total.LoginAttempts != 2 || total.Recordings != 1 || !slices.Equal(removed, []string{sha}) {
			t.Fatalf("delete B and C = %+v %v", total, removed)
		}
		for _, table := range []string{"sessions", "events", "event_data", "login_attempts", "commands", "tty_files"} {
			if n, err := db.NewSelect().TableExpr(table).Count(ctx); err != nil || n != 0 {
				t.Errorf("%s: %d rows left, %v", table, n, err)
			}
		}
	})
}
