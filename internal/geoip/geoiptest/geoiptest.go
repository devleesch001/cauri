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

// Network is a range of addresses with its location. A network with a
// city also gets its coordinates, as in city databases; with an ASN, its
// autonomous system, as in ASN databases.
type Network struct {
	CIDR, Country, City string
	Lat, Lon            float64
	ASN                 uint32
	ASOrg               string
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
		rec := mmdbtype.Map{}
		if n.Country != "" {
			rec["country"] = mmdbtype.Map{"iso_code": mmdbtype.String(n.Country)}
		}
		if n.ASN != 0 {
			rec["autonomous_system_number"] = mmdbtype.Uint32(n.ASN)
			rec["autonomous_system_organization"] = mmdbtype.String(n.ASOrg)
		}
		if n.City != "" {
			rec["city"] = mmdbtype.Map{"names": mmdbtype.Map{"en": mmdbtype.String(n.City)}}
			rec["location"] = mmdbtype.Map{"latitude": mmdbtype.Float64(n.Lat), "longitude": mmdbtype.Float64(n.Lon)}
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
