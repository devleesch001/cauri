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

// The key exchanges, networks and GeoIP databases of deleted sessions go
// once no other session uses them.
func TestPurgeSharedRows(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		shared := store.SSHKex{Digest: []byte("shared"), HASSH: ptr("h1")}
		own := store.SSHKex{Digest: []byte("own"), HASSH: ptr("h2")}
		for _, k := range []*store.SSHKex{&shared, &own} {
			if _, err := db.NewInsert().Model(k).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		net, err := store.NetworkID(ctx, db, 4134, ptr("Chinanet"))
		if err != nil {
			t.Fatal(err)
		}
		geo, err := store.GeoSourceID(ctx, db, "DBIP-City-Lite", t0)
		if err != nil {
			t.Fatal(err)
		}
		// Sessions 1 and 2 share a key exchange, a network and a database;
		// 1 has a key exchange of its own.
		for sess, kex := range map[string]int64{"1": own.ID, "2": shared.ID, "3": shared.ID} {
			q := db.NewUpdate().Model((*store.Session)(nil)).Set("ssh_kex_id = ?", kex).Where("cowrie_session = ?", sess)
			if sess != "3" {
				q = q.Set("network_id = ?", net).Set("geo_source_id = ?", geo)
			}
			if _, err := q.Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		count := func(model any) int {
			t.Helper()
			n, err := db.NewSelect().Model(model).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return int(n)
		}

		one := store.SessionFilter{SrcIP: "203.0.113.1"} // session 1
		if c, err := store.CountPurge(ctx, db, one); err != nil || c.KeyExchanges != 1 || c.Networks != 0 || c.GeoSources != 0 {
			t.Fatalf("count 1 = %+v, %v", c, err)
		}
		if c, _, err := store.DeleteSessionBatch(ctx, db, one, 10); err != nil || c.KeyExchanges != 1 {
			t.Fatalf("delete 1 = %+v, %v", c, err)
		}
		if count((*store.SSHKex)(nil)) != 1 || count((*store.Network)(nil)) != 1 || count((*store.GeoSource)(nil)) != 1 {
			t.Error("rows another session uses were deleted")
		}

		two := store.SessionFilter{SrcIP: "203.0.113.2"} // session 2, the last with the network and database
		c, _, err := store.DeleteSessionBatch(ctx, db, two, 10)
		if err != nil || c.KeyExchanges != 0 || c.Networks != 1 || c.GeoSources != 1 {
			t.Fatalf("delete 2 = %+v, %v", c, err)
		}
		if count((*store.SSHKex)(nil)) != 1 || count((*store.Network)(nil)) != 0 || count((*store.GeoSource)(nil)) != 0 {
			t.Error("rows no session uses were kept")
		}
	})
}
