package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// The seeded sessions (see seed): 1 at t0 on hp-01, root/admin then
// root/123456 accepted; 2 at t0+1h, admin/admin; 3 at t0+2h on hp-02,
// test/test. t0 is Wednesday 2026-10-14 09:00 UTC.

func TestSummarize(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		sum, err := store.Summarize(ctx, db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		want := store.Summary{Sessions: 3, Shells: 1, SourceIPs: 3, Logins: 4, Accepted: 1}
		if *sum != want {
			t.Errorf("summary = %+v, want %+v", *sum, want)
		}
		// A period compares to the one of equal length before it.
		sum, err = store.Summarize(ctx, db, store.StatsFilter{From: ptr(t0.Add(time.Hour)), To: ptr(t0.Add(3 * time.Hour))})
		if err != nil || sum.Sessions != 2 || sum.Logins != 2 || sum.Previous == nil || sum.Previous.Sessions != 1 || sum.Previous.Accepted != 1 {
			t.Errorf("period = %+v (previous %+v), %v", sum, sum.Previous, err)
		}
		// Empty: zeros, not errors.
		sum, err = store.Summarize(ctx, db, store.StatsFilter{Sensor: "nowhere"})
		if err != nil || *sum != (store.Summary{}) {
			t.Errorf("empty = %+v, %v", sum, err)
		}
	})
}

func TestActivity(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		a, err := store.GetActivity(context.Background(), db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		want := []store.HourCount{{Weekday: 3, Hour: 9, Sessions: 1}, {Weekday: 3, Hour: 10, Sessions: 1}, {Weekday: 3, Hour: 11, Sessions: 1}}
		if !slices.Equal(a.Hours, want) {
			t.Errorf("hours = %+v", a.Hours)
		}
		if len(a.Days) != 1 || a.Days[0].Day != "2026-10-14" || a.Days[0].Sessions != 3 || a.Days[0].Shells != 1 {
			t.Errorf("days = %+v", a.Days)
		}
	})
}

func TestCredentials(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		c, err := store.GetCredentials(ctx, db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if c.Attempts != 4 || c.Accepted != 1 || c.Usernames != 3 || c.Passwords != 3 {
			t.Errorf("counts = %+v", c)
		}
		if !slices.Equal(c.TopUsernames, []store.ValueCount{{"root", 2}, {"admin", 1}, {"test", 1}}) {
			t.Errorf("usernames = %+v", c.TopUsernames)
		}
		if !slices.Equal(c.TopPasswords, []store.ValueCount{{"admin", 2}, {"123456", 1}, {"test", 1}}) {
			t.Errorf("passwords = %+v", c.TopPasswords)
		}
		if len(c.TopPairs) != 4 || !slices.Equal(c.Worked, []store.PairCount{{Username: "root", Password: "123456", Count: 1, Accepted: 1}}) {
			t.Errorf("pairs = %+v, worked = %+v", c.TopPairs, c.Worked)
		}
		if !slices.Equal(c.Lengths, []store.LengthCount{{4, 1}, {5, 2}, {6, 1}}) {
			t.Errorf("lengths = %+v", c.Lengths)
		}
		if c, _ := store.GetCredentials(ctx, db, store.StatsFilter{Sensor: "hp-02"}); c.Attempts != 1 || c.TopUsernames[0].Value != "test" {
			t.Errorf("hp-02 = %+v", c)
		}
	})
}

func TestPayloads(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		s1, s3 := lookup(t, db, "hp-01", "1"), lookup(t, db, "hp-02", "3")
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("geo_country = 'NL'").
			Where("id = ?", s3).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		sha := "a8460f446be540410004b1a8db4083773fa46f7fe76fa84219c93daa1669f8f2"
		for _, x := range []store.Transfer{
			{SessionID: s1, TS: store.NewTime(t0.Add(time.Minute)), Direction: store.Download, URL: ptr("http://198.51.100.7/x.sh"), SHA256: &sha},
			{SessionID: s3, TS: store.NewTime(t0.Add(2*time.Hour + time.Minute)), Direction: store.Download, SHA256: &sha},
			{SessionID: s3, TS: store.NewTime(t0.Add(2*time.Hour + 2*time.Minute)), Direction: store.Upload, Filename: ptr("a")},
		} {
			if _, err := db.NewInsert().Model(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		p, err := store.GetPayloads(ctx, db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Sessions != 2 || p.Files != 1 || len(p.Items) != 1 {
			t.Fatalf("payloads = %+v", p)
		}
		it := p.Items[0]
		if it.SHA256 != sha || it.Transfers != 2 || it.Downloads != 2 || it.Sessions != 2 || it.Sources != 2 ||
			*it.URL != "http://198.51.100.7/x.sh" || !it.First.Equal(t0.Add(time.Minute)) || !slices.Equal(it.Countries, []string{"NL"}) {
			t.Errorf("payload = %+v", it)
		}
		if p, _ := store.GetPayloads(ctx, db, store.StatsFilter{Sensor: "hp-01"}); p.Items[0].Sessions != 1 {
			t.Errorf("payloads of hp-01 = %+v", p.Items)
		}
	})
}

func TestOrigins(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		o, err := store.GetOrigins(context.Background(), db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(o.Sources) != 3 || o.Sources[0].Sessions != 1 || o.Sources[0].IP != "203.0.113.1" || o.Sources[0].Shells != 1 {
			t.Errorf("sources = %+v", o.Sources)
		}
		if len(o.Clients) != 2 || o.Clients[0].Client != "SSH-2.0-OpenSSH_9.6" || o.Clients[1].Client != "SSH-2.0-libssh_0.9.6" {
			t.Errorf("clients = %+v", o.Clients)
		}
	})
}
