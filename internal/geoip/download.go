package geoip

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/devleesch001/cauri/internal/version"
)

// The databases cauri downloads by default: DB-IP Lite, free without an
// account (CC BY 4.0), published monthly under a name holding the month.
// An operator whose URL changed sets another, with or without {year} and
// {month}.
const (
	DefaultURL    = "https://download.db-ip.com/free/dbip-city-lite-{year}-{month}.mmdb.gz"
	DefaultASNURL = "https://download.db-ip.com/free/dbip-asn-lite-{year}-{month}.mmdb.gz"
)

// MaxDownloadSize bounds a downloaded database, decompressed: DB-IP City
// Lite is about 130 MB.
const MaxDownloadSize = 1 << 30

// ErrWrongType means a downloaded file is a database of another kind, such
// as an ASN database where a location one is expected.
var ErrWrongType = errors.New("not the expected kind of database")

// now is the clock that picks the month of a URL; tests change it.
var now = time.Now

// URLs returns the addresses tried for a URL: with {year} and {month}, the
// current month (UTC) then the previous one, which DB-IP may not have
// replaced yet in the first days of a month.
func URLs(tmpl string) []string {
	if !strings.Contains(tmpl, "{year}") && !strings.Contains(tmpl, "{month}") {
		return []string{tmpl}
	}
	t := now().UTC()
	out := make([]string, 0, 2)
	for _, m := range []time.Time{t, time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)} {
		out = append(out, strings.NewReplacer("{year}", m.Format("2006"), "{month}", m.Format("01")).Replace(tmpl))
	}
	return out
}

// Download fetches the database at tmpl (see URLs), gzipped or not, into
// path: written beside it, checked to be an MMDB database of the expected
// kind (ASN or location), then renamed over it, so that the file in place
// is never left half written or of the wrong kind. progress, if not nil,
// is told the bytes received and expected (-1 if unknown).
func Download(ctx context.Context, client *http.Client, tmpl, path string, asn bool, progress func(done, total int64)) (Info, error) {
	urls := URLs(tmpl)
	var resp *http.Response
	for i, u := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return Info{}, err
		}
		req.Header.Set("User-Agent", "cauri/"+version.Get().Tag)
		resp, err = client.Do(req) //nolint:gosec // G704: the URL is the operator's setting
		if err != nil {
			return Info{}, err
		}
		if resp.StatusCode == http.StatusOK {
			break
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound || i == len(urls)-1 {
			return Info{}, fmt.Errorf("%s: %s", strings.Join(urls[:i+1], ", then "), resp.Status)
		}
	}
	defer func() { _ = resp.Body.Close() }()

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Info{}, err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return Info{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	err = save(tmp, &counter{r: resp.Body, total: resp.ContentLength, progress: progress})
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Info{}, fmt.Errorf("%s: %w", resp.Request.URL, err)
	}

	info, err := check(tmp.Name(), asn)
	if err != nil {
		return Info{}, fmt.Errorf("%s: %w", resp.Request.URL, err)
	}
	// Readable by the API whoever downloads it: the data is public.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // G302: see above
		return Info{}, err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return Info{}, err
	}
	return info, nil
}

// save writes body to f, decompressed if gzipped, at most MaxDownloadSize
// bytes.
func save(f *os.File, body io.Reader) error {
	br := bufio.NewReader(body)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer func() { _ = gz.Close() }()
		r = gz
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxDownloadSize+1))
	if err != nil {
		return err
	}
	if n > MaxDownloadSize {
		return fmt.Errorf("larger than %d bytes", MaxDownloadSize)
	}
	return nil
}

// check opens a downloaded file and tells what it is: an ASN database if
// asn, else a city or country one.
func check(path string, asn bool) (Info, error) {
	r, err := maxminddb.Open(path)
	if err != nil {
		return Info{}, fmt.Errorf("not an MMDB database: %w", err)
	}
	defer func() { _ = r.Close() }()
	info := infoOf(r)
	t := strings.ToLower(info.Type)
	isASN := strings.Contains(t, "asn")
	isLocation := strings.Contains(t, "city") || strings.Contains(t, "country")
	if asn && !isASN || !asn && (isASN || !isLocation) {
		return info, fmt.Errorf("%w: %s", ErrWrongType, info.Type)
	}
	return info, nil
}

// counter counts the bytes read, for progress.
type counter struct {
	r        io.Reader
	done     int64
	total    int64
	progress func(done, total int64)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.done += int64(n)
	if c.progress != nil && n > 0 {
		c.progress(c.done, c.total)
	}
	return n, err
}
