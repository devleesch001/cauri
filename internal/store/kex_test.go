package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// An attacker told by its key exchange: sessions 1 and 3 share one, from
// two addresses; 2 has another of the same HASSH.
func TestKex(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		shared := store.SSHKex{Digest: []byte("a"), HASSH: ptr("h1"), KexAlgs: ptr("curve25519-sha256"), KeyAlgs: ptr("ssh-ed25519")}
		other := store.SSHKex{Digest: []byte("b"), HASSH: ptr("h1"), KeyAlgs: ptr("ssh-rsa")}
		for _, k := range []*store.SSHKex{&shared, &other} {
			if _, err := db.NewInsert().Model(k).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for sess, id := range map[string]int64{"1": shared.ID, "2": other.ID, "3": shared.ID} {
			if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("ssh_kex_id = ?", id).
				Where("cowrie_session = ?", sess).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// Session 1 offered a public key, twice.
		s1 := lookup(t, db, "hp-01", "1")
		key := `{"fingerprint":"cd:7a","key":"ssh-ed25519 AAAA","type":"ssh-ed25519","username":"root"}`
		for i := range 2 {
			e := store.Event{Hash: []byte{0xfe, byte(i)}, SensorID: 1, SessionID: &s1, Type: "cowrie.client.fingerprint",
				TS: store.NewTime(t0.Add(time.Duration(i) * time.Second)), ReceivedAt: store.NewTime(t0), Data: &key}
			if _, err := db.NewInsert().Model(&e).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}

		rows, total, err := store.ListKex(ctx, db, store.KexFilter{})
		if err != nil || total != 2 || rows[0].ID != shared.ID || rows[0].Sessions != 2 || rows[0].Sources != 2 ||
			rows[0].LoggedIn != 1 || rows[0].Logins != 3 || *rows[0].KeyAlgs != "ssh-ed25519" {
			t.Fatalf("key exchanges = %+v, %d, %v", rows, total, err)
		}
		// Filters and sorts.
		if rows, _, _ := store.ListKex(ctx, db, store.KexFilter{Sort: store.Sort{By: store.SortSessions, Asc: true}}); rows[0].ID != other.ID {
			t.Errorf("fewest sessions first = %+v", rows)
		}
		if _, n, _ := store.ListKex(ctx, db, store.KexFilter{StatsFilter: store.StatsFilter{Sensor: "hp-02"}}); n != 1 {
			t.Errorf("key exchanges of hp-02 = %d", n)
		}
		if _, n, _ := store.ListKex(ctx, db, store.KexFilter{LoggedIn: ptr(true)}); n != 1 {
			t.Errorf("key exchanges that logged in = %d", n)
		}
		if _, n, _ := store.ListKex(ctx, db, store.KexFilter{HASSH: "nope"}); n != 0 {
			t.Errorf("other HASSH = %d", n)
		}

		d, err := store.GetKex(ctx, db, shared.ID, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if *d.KexAlgs != "curve25519-sha256" || len(d.Addresses) != 2 || !slices.Equal(d.Sensors, []string{"hp-01", "hp-02"}) ||
			len(d.Variants) != 1 || d.Variants[0].ID != other.ID || d.Variants[0].Sessions != 1 ||
			len(d.Keys) != 1 || d.Keys[0].Fingerprint != "cd:7a" || d.Keys[0].Sessions != 1 {
			t.Errorf("key exchange = %+v", d)
		}
		if _, err := store.GetKex(ctx, db, shared.ID, store.StatsFilter{Sensor: "nope"}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("no session: %v", err)
		}

		// The sessions, statistics and address of a key exchange.
		if _, got := sessions(t, db, store.SessionFilter{Kex: shared.ID}); got != "31" {
			t.Errorf("sessions of the key exchange = %q", got)
		}
		if sum, err := store.Summarize(ctx, db, store.StatsFilter{Kex: shared.ID}); err != nil || sum.Sessions != 2 {
			t.Errorf("summary = %+v, %v", sum, err)
		}
		a, err := store.GetAttacker(ctx, db, "203.0.113.1", store.StatsFilter{})
		if err != nil || len(a.Kex) != 1 || a.Kex[0].ID != shared.ID || a.Kex[0].Sessions != 1 || len(a.Keys) != 1 {
			t.Errorf("attacker = %+v, %v", a, err)
		}
	})
}
