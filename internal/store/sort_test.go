package store_test

import (
	"context"
	"slices"
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// Text columns are compared bytewise by SQLite and by the database's
// collation by PostgreSQL: the cases below agree on both.
func TestSortSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		for name, c := range map[string]struct {
			sort store.Sort
			want []string // Cowrie sessions: A is 1, B 2, C 3
		}{
			"default, latest first": {store.Sort{}, []string{"3", "2", "1"}},
			"oldest first":          {store.Sort{Asc: true}, []string{"1", "2", "3"}},
			"source":                {store.Sort{By: "source", Asc: true}, []string{"1", "2", "3"}},
			// Only A has a duration: the others last, both ways.
			"longest first":   {store.Sort{By: "duration"}, []string{"1", "3", "2"}},
			"shortest first":  {store.Sort{By: "duration", Asc: true}, []string{"1", "3", "2"}},
			"most commands":   {store.Sort{By: "commands"}, []string{"1", "3", "2"}},
			"fewest commands": {store.Sort{By: "commands", Asc: true}, []string{"3", "2", "1"}},
			"unknown column":  {store.Sort{By: "s.id; --"}, []string{"3", "2", "1"}},
		} {
			rows, _, err := store.ListSessions(context.Background(), db, store.SessionFilter{Sort: c.sort})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got := make([]string, len(rows))
			for i, r := range rows {
				got[i] = r.CowrieSession
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
	})
}

func TestSortLoginAttempts(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		for name, c := range map[string]struct {
			sort store.Sort
			want []string
		}{
			"latest first": {store.Sort{}, []string{"test/test", "admin/admin", "root/123456", "root/admin"}},
			"username":     {store.Sort{By: "username", Asc: true}, []string{"admin/admin", "root/123456", "root/admin", "test/test"}},
			"password":     {store.Sort{By: "password", Asc: true}, []string{"root/123456", "admin/admin", "root/admin", "test/test"}},
		} {
			rows, _, err := store.ListLoginAttempts(context.Background(), db, store.LoginAttemptFilter{Sort: c.sort})
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			got := make([]string, len(rows))
			for i, r := range rows {
				got[i] = r.Username + "/" + r.Password
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
	})
}

func TestSortAttackers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seedAttackers(t, db)
		// Fewest sessions first; ties by most sessions, then latest seen.
		f := store.AttackerFilter{Sort: store.Sort{By: store.SortSessions, Asc: true}}
		if _, ips, _ := attackers(t, db, f); !slices.Equal(ips, []string{"203.0.113.3", "203.0.113.2", "203.0.113.1"}) {
			t.Errorf("fewest sessions first: %v", ips)
		}
		f.Sort = store.Sort{By: store.SortLastSeen, Asc: true}
		if _, ips, _ := attackers(t, db, f); !slices.Equal(ips, []string{"203.0.113.2", "203.0.113.3", "203.0.113.1"}) {
			t.Errorf("earliest seen first: %v", ips)
		}
	})
}
