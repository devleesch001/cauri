package geoip

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/geoip/geoiptest"
)

func TestURLs(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC) }
	got := URLs(DefaultURL)
	want := []string{
		"https://download.db-ip.com/free/dbip-city-lite-2026-01.mmdb.gz",
		"https://download.db-ip.com/free/dbip-city-lite-2025-12.mmdb.gz",
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("URLs = %v", got)
	}
	if got := URLs("https://mirror.example/geo.mmdb"); len(got) != 1 {
		t.Errorf("fixed URL = %v", got)
	}
}

func mmdb(t *testing.T, dbType string, gzipped bool) []byte {
	t.Helper()
	data, err := os.ReadFile(geoiptest.Write(t, t.TempDir(), dbType, oct1,
		geoiptest.Network{CIDR: "5.160.0.0/16", Country: "IR", ASN: 58224, ASOrg: "TCI"}))
	if err != nil {
		t.Fatal(err)
	}
	if !gzipped {
		return data
	}
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	_, _ = gz.Write(data)
	_ = gz.Close()
	return b.Bytes()
}

func TestDownload(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) }
	files := map[string][]byte{
		// November is not published yet: October is taken.
		"/city-2026-10.mmdb.gz": mmdb(t, "DBIP-City-Lite", true),
		"/asn.mmdb":             mmdb(t, "DBIP-ASN-Lite (compat=GeoLite2-ASN)", false),
		"/broken.mmdb":          []byte("not a database"),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		if r.URL.Path == "/error" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "geoip.mmdb")

	var done int64
	info, err := Download(ctx, srv.Client(), srv.URL+"/city-{year}-{month}.mmdb.gz", path, false,
		func(d, _ int64) { done = d })
	if err != nil || info.Type != "DBIP-City-Lite" || !info.Built.Equal(oct1) {
		t.Fatalf("Download = %+v, %v", info, err)
	}
	if done != int64(len(files["/city-2026-10.mmdb.gz"])) {
		t.Errorf("progress %d bytes", done)
	}
	d, err := Open(path, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if loc, _, ok := d.Locate("5.160.0.1"); !ok || loc.Country != "IR" {
		t.Errorf("downloaded database: %+v %v", loc, ok)
	}
	_ = d.Close()
	before, _ := os.ReadFile(path)

	// Every failure leaves the database in place, and nothing beside it.
	for name, c := range map[string]struct {
		url string
		asn bool
	}{
		"asn as location": {"/asn.mmdb", false},
		"location as asn": {"/city-2026-10.mmdb.gz", true},
		"broken":          {"/broken.mmdb", false},
		"not found":       {"/missing-{year}-{month}.mmdb", false},
		"server error":    {"/error", false},
	} {
		_, err := Download(ctx, srv.Client(), srv.URL+c.url, path, c.asn, nil)
		if err == nil {
			t.Errorf("%s: no error", name)
		}
		if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
			t.Errorf("%s: the database changed", name)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 1 {
			t.Errorf("%s: %d files left in the directory", name, len(entries))
		}
	}
	if _, err := Download(ctx, srv.Client(), srv.URL+"/asn.mmdb", path, false, nil); !errors.Is(err, ErrWrongType) {
		t.Errorf("wrong type: %v", err)
	}

	// An ASN database, not gzipped, into a directory to create.
	asnPath := filepath.Join(dir, "sub", "asn.mmdb")
	if info, err := Download(ctx, srv.Client(), srv.URL+"/asn.mmdb", asnPath, true, nil); err != nil || info.Provider != "db-ip" {
		t.Errorf("ASN download = %+v, %v", info, err)
	}
}
