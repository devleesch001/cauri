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

func TestProvider(t *testing.T) {
	for in, want := range map[string]string{
		"DBIP-City-Lite":                      "db-ip",
		"DBIP-ASN-Lite (compat=GeoLite2-ASN)": "db-ip",
		"DBIP-City-Lite 2026-10-01":           "db-ip", // a Source
		"GeoLite2-City":                       "maxmind",
		"GeoIP2-Country":                      "maxmind",
		"ipinfo":                              "",
		"":                                    "",
	} {
		if got := Provider(in); got != want {
			t.Errorf("Provider(%q) = %q, want %q", in, got, want)
		}
	}
}

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
		if loc != c.loc || ok != c.ok || src.Source() != "DBIP-City-Lite 2026-10-01" {
			t.Errorf("%s: %+v %q %v", ip, loc, src.Source(), ok)
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
		if loc.Country == "AF" && src.Source() == "GeoLite2-City 2026-11-01" && d.Info().Provider == "maxmind" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not reloaded: %+v %q", loc, src.Source())
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

// A missing file leaves the database empty until it appears.
func TestOpenMissing(t *testing.T) {
	dir := t.TempDir()
	d, err := Open(dir+"/geo.mmdb", quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if _, source, ok := d.Locate("5.160.0.1"); ok || d.Loaded() || source != (Info{}) {
		t.Errorf("empty database answers: %v %v %+v", ok, d.Loaded(), source)
	}
	if _, ok := d.Network("5.160.0.1"); ok {
		t.Error("empty database finds a network")
	}
	loaded := make(chan struct{})
	d.OnLoad(func() { close(loaded) })
	geoiptest.Write(t, dir, "DBIP-City-Lite", oct1, geoiptest.Network{CIDR: "5.160.0.0/16", Country: "IR"})
	if err := d.Reload(); err != nil {
		t.Fatal(err)
	}
	<-loaded
	if loc, _, ok := d.Locate("5.160.0.1"); !ok || loc.Country != "IR" {
		t.Errorf("after the file appeared: %+v %v", loc, ok)
	}
}

func TestOpenErrors(t *testing.T) {
	bad := t.TempDir() + "/bad.mmdb"
	_ = os.WriteFile(bad, []byte("not mmdb"), 0o600)
	if _, err := Open(bad, quiet); err == nil {
		t.Error("want error for an invalid file")
	}
}

// ASN databases (DB-IP ASN Lite, GeoLite2-ASN) give networks, no location.
func TestNetwork(t *testing.T) {
	path := geoiptest.Write(t, t.TempDir(), "DBIP-ASN-Lite (compat=GeoLite2-ASN)", oct1,
		geoiptest.Network{CIDR: "109.160.32.0/24", ASN: 197170, ASOrg: "TechTies Inc."})
	d, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if i := d.Info(); i.Provider != "db-ip" {
		t.Errorf("info = %+v", i)
	}
	if n, ok := d.Network("109.160.32.169"); !ok || n != (Network{197170, "TechTies Inc."}) {
		t.Errorf("network = %+v %v", n, ok)
	}
	if _, ok := d.Network("203.0.113.7"); ok {
		t.Error("unknown address has a network")
	}
	if _, _, ok := d.Locate("109.160.32.169"); ok {
		t.Error("an ASN database locates no country")
	}
}
