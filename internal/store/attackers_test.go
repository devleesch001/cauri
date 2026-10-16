package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// seedAttackers adds to seed a session D on hp-02 at t0+3h from the address
// of A (203.0.113.1), a failed login: A and D are one attacker.
func seedAttackers(t *testing.T, db *bun.DB) {
	t.Helper()
	svc := seed(t, db)
	b := protocol.EventBatch{SensorID: "hp-02"}
	for i, e := range []ev{
		{"eventid": "cowrie.session.connect"},
		{"eventid": "cowrie.login.failed", "username": "root", "password": "toor"},
	} {
		e["session"] = "4"
		e["src_ip"] = "203.0.113.1"
		e["timestamp"] = t0.Add(3*time.Hour + time.Duration(i)*time.Second).Format(time.RFC3339Nano)
		raw, _ := json.Marshal(e)
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-02", raw), Raw: raw})
	}
	if _, err := svc.IngestEvents(context.Background(), "hp-02", b); err != nil {
		t.Fatal(err)
	}
}

func attackers(t *testing.T, db *bun.DB, f store.AttackerFilter) ([]store.Attacker, []string, int) {
	t.Helper()
	rows, total, err := store.ListAttackers(context.Background(), db, f)
	if err != nil {
		t.Fatal(err)
	}
	ips := make([]string, len(rows))
	for i, r := range rows {
		ips[i] = r.IP
	}
	return rows, ips, total
}

func TestListAttackers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seedAttackers(t, db)
		rows, ips, total := attackers(t, db, store.AttackerFilter{})
		// Most sessions first, then the latest seen.
		if total != 3 || !slices.Equal(ips, []string{"203.0.113.1", "203.0.113.3", "203.0.113.2"}) {
			t.Fatalf("attackers = %v, total %d", ips, total)
		}
		a := rows[0]
		if a.Sessions != 2 || a.LoggedIn != 1 || a.Logins != 3 || !a.FirstSeen.Equal(t0) || !a.LastSeen.Equal(t0.Add(3*time.Hour)) {
			t.Errorf("203.0.113.1 = %+v", a)
		}

		for name, c := range map[string]struct {
			f    store.AttackerFilter
			want []string
		}{
			"last seen":             {store.AttackerFilter{Sort: store.Sort{By: store.SortLastSeen}}, []string{"203.0.113.1", "203.0.113.3", "203.0.113.2"}},
			"logged in, most first": {store.AttackerFilter{Sort: store.Sort{By: store.SortLoggedIn}}, []string{"203.0.113.1", "203.0.113.3", "203.0.113.2"}},
			"logged in":             {store.AttackerFilter{LoggedIn: ptr(true)}, []string{"203.0.113.1"}},
			"never":                 {store.AttackerFilter{LoggedIn: ptr(false)}, []string{"203.0.113.3", "203.0.113.2"}},
			"prefix":                {store.AttackerFilter{IPPrefix: "203.0.113.3"}, []string{"203.0.113.3"}},
			"wildcards":             {store.AttackerFilter{IPPrefix: "203_0%"}, []string{}},
			// The statistics filter applies to sessions: from hp-02, the
			// first attacker has one session, one login.
			"sensor": {store.AttackerFilter{StatsFilter: store.StatsFilter{Sensor: "hp-02"}}, []string{"203.0.113.1", "203.0.113.3"}},
		} {
			if _, got, _ := attackers(t, db, c.f); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
		if rows, _, _ := attackers(t, db, store.AttackerFilter{StatsFilter: store.StatsFilter{Sensor: "hp-02"}}); rows[0].Sessions != 1 || rows[0].Logins != 1 {
			t.Errorf("first attacker on hp-02 = %+v", rows[0])
		}
		if _, ips, total := attackers(t, db, store.AttackerFilter{Page: store.Page{Limit: 1, Offset: 1}}); total != 3 || !slices.Equal(ips, []string{"203.0.113.3"}) {
			t.Errorf("page = %v, total %d", ips, total)
		}
	})
}

func TestGetAttacker(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seedAttackers(t, db)
		ctx := context.Background()
		d, err := store.GetAttacker(ctx, db, "203.0.113.1", store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if d.Sessions != 2 || d.Logins != 3 || !slices.Equal(d.Sensors, []string{"hp-01", "hp-02"}) ||
			len(d.Clients) != 1 || d.Clients[0].Client != "SSH-2.0-OpenSSH_9.6" || len(d.Kex) != 0 {
			t.Errorf("attacker = %+v", d)
		}
		if _, err := store.GetAttacker(ctx, db, "198.51.100.1", store.StatsFilter{}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("unknown address: %v", err)
		}
		// Out of the period: nothing from it.
		after := t0.Add(24 * time.Hour)
		if _, err := store.GetAttacker(ctx, db, "203.0.113.1", store.StatsFilter{From: &after}); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("out of the period: %v", err)
		}

		// Every statistic narrows to one attacker.
		sum, err := store.Summarize(ctx, db, store.StatsFilter{SrcIP: "203.0.113.1"})
		if err != nil || sum.Sessions != 2 || sum.Logins != 3 || sum.Accepted != 1 || sum.SourceIPs != 1 {
			t.Errorf("summary of 203.0.113.1 = %+v, %v", sum, err)
		}
	})
}
