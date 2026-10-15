package geoip

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/geoip/geoiptest"
)

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	oct1  = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
)

func TestLocate(t *testing.T) {
	path := geoiptest.Write(t, t.TempDir(), "DBIP-City-Lite", oct1,
		geoiptest.Network{CIDR: "5.160.0.0/16", Country: "IR", City: "Tehran", Lat: 35.6944, Lon: 51.4215},
		geoiptest.Network{CIDR: "2.13.0.0/16", Country: "FR"},
	)
	d, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()

	if i := d.Info(); i.Provider != "db-ip" || i.Source() != "DBIP-City-Lite 2026-10-01" {
		t.Errorf("info = %+v", i)
	}
	tehran := Location{Country: "IR", City: "Tehran", Lat: 35.6944, Lon: 51.4215, HasCoords: true}
	cases := map[string]struct {
		loc Location
		ok  bool
	}{
		"5.160.3.4":        {tehran, true},
		"2.13.0.1":         {Location{Country: "FR"}, true}, // no city, no coordinates
		"::ffff:5.160.3.4": {tehran, true},                  // IPv4-mapped
		"203.0.113.7":      {Location{}, false},             // not in the database
		"not an ip":        {Location{}, false},
	}
	for ip, c := range cases {
		loc, src, ok := d.Locate(ip)
		if loc != c.loc || ok != c.ok || (ok && src != "DBIP-City-Lite 2026-10-01") {
			t.Errorf("%s: %+v %q %v", ip, loc, src, ok)
		}
	}
}

// Replacing the file updates the locations without restarting.
func TestWatchReloads(t *testing.T) {
	dir := t.TempDir()
	path := geoiptest.Write(t, dir, "DBIP-City-Lite", oct1, geoiptest.Network{CIDR: "5.160.0.0/16", Country: "IR"})
	d, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go d.Watch(ctx, 10*time.Millisecond)

	nov1 := oct1.AddDate(0, 1, 0)
	time.Sleep(20 * time.Millisecond) // a distinct modification time
	geoiptest.Write(t, dir, "GeoLite2-City", nov1, geoiptest.Network{CIDR: "5.160.0.0/16", Country: "AF"})
	deadline := time.Now().Add(2 * time.Second)
	for {
		loc, src, _ := d.Locate("5.160.0.1")
		if loc.Country == "AF" && src == "GeoLite2-City 2026-11-01" && d.Info().Provider == "maxmind" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reloaded: %+v %q", loc, src)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Overwriting the file in place (cp) with a broken one keeps the
	// current database: it is in memory, not mapped from the file.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if loc, _, ok := d.Locate("5.160.0.1"); !ok || loc.Country != "AF" {
		t.Errorf("after a broken file: %+v %v", loc, ok)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(t.TempDir()+"/missing.mmdb", quiet); err == nil {
		t.Error("want error for a missing file")
	}
	bad := t.TempDir() + "/bad.mmdb"
	_ = os.WriteFile(bad, []byte("not mmdb"), 0o600)
	if _, err := Open(bad, quiet); err == nil {
		t.Error("want error for an invalid file")
	}
}
