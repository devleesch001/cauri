// Package geoip locates IP addresses with an MMDB database, such as DB-IP
// Lite or MaxMind GeoLite2, which share the same record structure, and
// finds their network (autonomous system) with an ASN database of either
// provider. The
// database is not part of cauri: the operator provides the file, and
// replaces it to update it; it is reloaded when it changes.
//
// The file is read into memory rather than memory-mapped: overwriting a
// mapped file in place (a plain cp) would change, or truncate, the data
// under the reader, up to crashing the process with SIGBUS. The cost is
// memory equal to the file size.
package geoip

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

// Location is where an address is, as far as the database knows.
type Location struct {
	Country string // ISO 3166-1 alpha-2 code, such as "IR"
	City    string // in English, empty if the database has no cities
	// Lat and Lon are the coordinates of the city (or region), set only if
	// HasCoords: country databases have none.
	Lat, Lon  float64
	HasCoords bool
}

// Info describes the loaded database, for display and attribution.
type Info struct {
	Type  string    `json:"type"`  // such as "DBIP-City-Lite" or "GeoLite2-City"
	Built time.Time `json:"built"` // build date of the database
	// Provider is "db-ip", "maxmind" or "", each with its own attribution.
	Provider string `json:"provider"`
}

// Source names the database for records of where a location came from,
// such as "DBIP-City-Lite 2026-10-01".
func (i Info) Source() string {
	return i.Type + " " + i.Built.UTC().Format(time.DateOnly)
}

// Provider returns who publishes databases of a type ("DBIP-City-Lite",
// "GeoLite2-ASN") or of a Source: "db-ip", "maxmind" or "" if unknown.
// It sets the attribution their data requires.
func Provider(typeOrSource string) string {
	t, _, _ := strings.Cut(strings.ToLower(typeOrSource), " ")
	switch {
	case strings.HasPrefix(t, "dbip"):
		return "db-ip"
	case strings.HasPrefix(t, "geolite"), strings.HasPrefix(t, "geoip"):
		return "maxmind"
	}
	return ""
}

// record is the part of an MMDB city or country record cauri uses.
type record struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	City struct {
		Names map[string]string `maxminddb:"names"`
	} `maxminddb:"city"`
	Location struct {
		Latitude  *float64 `maxminddb:"latitude"`
		Longitude *float64 `maxminddb:"longitude"`
	} `maxminddb:"location"`
	// ASN databases.
	ASN   uint32 `maxminddb:"autonomous_system_number"`
	ASOrg string `maxminddb:"autonomous_system_organization"`
}

// Network is the autonomous system an address belongs to.
type Network struct {
	ASN uint32 // such as 4134
	Org string // such as "Chinanet"
}

// DB is an MMDB database, reloaded when its file changes.
type DB struct {
	path string
	log  *slog.Logger

	mu     sync.RWMutex
	reader *maxminddb.Reader
	info   Info
	mtime  time.Time
}

// Open opens the database at path.
func Open(path string, log *slog.Logger) (*DB, error) {
	d := &DB{path: path, log: log}
	if err := d.load(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DB) load() error {
	fi, err := os.Stat(d.path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(d.path) //nolint:gosec // G304: path given by the operator
	if err != nil {
		return err
	}
	r, err := maxminddb.OpenBytes(data)
	if err != nil {
		return fmt.Errorf("geoip database %s: %w", d.path, err)
	}
	info := Info{Type: r.Metadata.DatabaseType, Built: time.Unix(int64(r.Metadata.BuildEpoch), 0).UTC()} //nolint:gosec // G115: an epoch fits int64
	info.Provider = Provider(info.Type)

	d.mu.Lock()
	old := d.reader
	d.reader, d.info, d.mtime = r, info, fi.ModTime()
	d.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	d.log.Info("geoip database loaded", "path", d.path, "source", info.Source())
	return nil
}

// Info describes the loaded database.
func (d *DB) Info() Info {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.info
}

// Locate returns the location of ip and the database that answered. ok is
// false when the address is invalid or unknown to the database (private
// and reserved ranges are).
func (d *DB) Locate(ip string) (loc Location, source Info, ok bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	source = d.info
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return loc, source, false
	}
	var rec record
	res := d.reader.Lookup(addr.Unmap())
	if !res.Found() || res.Decode(&rec) != nil || rec.Country.ISOCode == "" {
		return loc, source, false
	}
	loc = Location{Country: rec.Country.ISOCode, City: rec.City.Names["en"]}
	if lat, lon := rec.Location.Latitude, rec.Location.Longitude; lat != nil && lon != nil {
		loc.Lat, loc.Lon, loc.HasCoords = *lat, *lon, true
	}
	return loc, source, true
}

// Network returns the autonomous system of ip; ok is false when the
// address is invalid, unknown, or the database has no networks.
func (d *DB) Network(ip string) (n Network, ok bool) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return n, false
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	var rec record
	res := d.reader.Lookup(addr.Unmap())
	if !res.Found() || res.Decode(&rec) != nil || rec.ASN == 0 {
		return n, false
	}
	return Network{ASN: rec.ASN, Org: rec.ASOrg}, true
}

// Watch reloads the database when its file changes, checking every
// interval, until ctx is done. A file being replaced may be momentarily
// unreadable: the current database stays in use and the next check
// retries.
func (d *DB) Watch(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fi, err := os.Stat(d.path)
		d.mu.RLock()
		changed := err == nil && !fi.ModTime().Equal(d.mtime)
		d.mu.RUnlock()
		if !changed {
			continue
		}
		if err := d.load(); err != nil {
			d.log.Warn("geoip database not reloaded", "err", err)
		}
	}
}

// Close releases the database.
func (d *DB) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.reader == nil {
		return nil
	}
	err := d.reader.Close()
	d.reader = nil
	return err
}
