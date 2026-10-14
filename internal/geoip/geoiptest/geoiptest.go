// Package geoiptest writes small MMDB databases for tests, so that no
// third-party database is committed.
package geoiptest

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
)

// Network is a range of addresses with its location.
type Network struct {
	CIDR, Country, City string
}

// Write writes a database of the given type (such as "DBIP-City-Lite")
// built at built, into dir, and returns its path.
func Write(t testing.TB, dir, dbType string, built time.Time, networks ...Network) string {
	t.Helper()
	// Tests use documentation ranges (203.0.113.0/24) as public addresses:
	// reserved networks must be allowed.
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		DatabaseType: dbType, RecordSize: 24, BuildEpoch: built.Unix(), IncludeReservedNetworks: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range networks {
		_, ipnet, err := net.ParseCIDR(n.CIDR)
		if err != nil {
			t.Fatal(err)
		}
		rec := mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(n.Country)}}
		if n.City != "" {
			rec["city"] = mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String(n.City)}}
		}
		if err := tree.Insert(ipnet, rec); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "geo.mmdb")
	tmp := path + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec // G304: a test directory
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.WriteTo(f); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// Replace atomically, as an operator updating the database would.
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	return path
}
