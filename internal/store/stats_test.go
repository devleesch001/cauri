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
		want := store.Summary{Sessions: 3, LoggedIn: 1, SourceIPs: 3, Logins: 4, Accepted: 1}
		if *sum != want {
			t.Errorf("summary = %+v, want %+v", *sum, want)
		}
		// A period compares to the one of equal length before it.
		sum, err = store.Summarize(ctx, db, store.StatsFilter{From: ptr(t0.Add(time.Hour)), To: ptr(t0.Add(3 * time.Hour))})
		if err != nil || sum.Sessions != 2 || sum.Logins != 2 || sum.Previous == nil || sum.Previous.Sessions != 1 || sum.Previous.Accepted != 1 {
			t.Errorf("period = %+v (previous %+v), %v", sum, sum.Previous, err)
		}
		// Only the sessions that logged in: A, its two attempts.
		sum, err = store.Summarize(ctx, db, store.StatsFilter{Login: store.LoginSuccess})
		if err != nil || sum.Sessions != 1 || sum.LoggedIn != 1 || sum.Logins != 2 || sum.Accepted != 1 {
			t.Errorf("shells only = %+v, %v", sum, err)
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
		if len(a.Days) != 1 || a.Days[0].Day != "2026-10-14" || a.Days[0].Sessions != 3 || a.Days[0].LoggedIn != 1 {
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
		// From a file to the sessions that transferred it.
		if _, got := sessions(t, db, store.SessionFilter{File: sha}); got != "31" {
			t.Errorf("sessions with the file = %q", got)
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
		if len(o.Sources) != 3 || o.Sources[0].Sessions != 1 || o.Sources[0].IP != "203.0.113.1" || o.Sources[0].LoggedIn != 1 {
			t.Errorf("sources = %+v", o.Sources)
		}
		if len(o.Networks) != 0 {
			t.Errorf("networks without an ASN database = %+v", o.Networks)
		}
		ctx := context.Background()
		for sess, asn := range map[string]int64{"1": 4134, "2": 4134} {
			if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("asn = ?", asn).Set("as_org = 'Chinanet'").
				Where("id = ?", lookup(t, db, "hp-01", sess)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		o, _ = store.GetOrigins(ctx, db, store.StatsFilter{})
		if len(o.Networks) != 1 || o.Networks[0] != (store.NetworkCount{ASN: 4134, Org: "Chinanet", Sessions: 2, Sources: 2}) {
			t.Errorf("networks = %+v", o.Networks)
		}
		if _, got := sessions(t, db, store.SessionFilter{ASN: 4134}); got != "21" {
			t.Errorf("sessions of AS4134 = %q", got)
		}
		if len(o.Clients) != 2 || o.Clients[0].Client != "SSH-2.0-OpenSSH_9.6" || o.Clients[1].Client != "SSH-2.0-libssh_0.9.6" {
			t.Errorf("clients = %+v", o.Clients)
		}
	})
}

func TestCommands(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		c, err := store.GetCommands(ctx, db, store.StatsFilter{})
		if err != nil {
			t.Fatal(err)
		}
		// Session 1 ran uname -a and cat /proc/cpuinfo; it has no HASSH.
		if c.Total != 2 || c.Distinct != 2 || c.Sessions != 1 || len(c.Top) != 2 || c.Top[0].Sessions != 1 || len(c.HASSH) != 0 {
			t.Errorf("commands = %+v", c)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("hassh = 'abc'").
			Where("id = ?", lookup(t, db, "hp-01", "1")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		c, _ = store.GetCommands(ctx, db, store.StatsFilter{})
		if len(c.HASSH) != 1 || c.HASSH[0] != (store.HASSHCount{HASSH: "abc", Client: "SSH-2.0-OpenSSH_9.6", Clients: 1, Sessions: 1, Sources: 1}) {
			t.Errorf("hassh = %+v", c.HASSH)
		}
		if _, got := sessions(t, db, store.SessionFilter{Command: "uname -a"}); got != "1" {
			t.Errorf("sessions that ran uname -a = %q", got)
		}
		if _, got := sessions(t, db, store.SessionFilter{Command: "uname"}); got != "" {
			t.Errorf("a command filter matches exactly: %q", got)
		}
		if _, got := sessions(t, db, store.SessionFilter{HASSH: "abc"}); got != "1" {
			t.Errorf("sessions with HASSH abc = %q", got)
		}
	})
}
