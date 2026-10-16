package api

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/jobs"
)

// The third-party databases the administration downloads on request,
// never on its own: the GeoIP databases (package geoip).

// Download is a database the administration can download.
type Download struct {
	Name string // in the routes and tasks: "geoip" or "asn"
	URL  string // where from, see geoip.URLs
	ASN  bool   // an ASN database, else a location one
	DB   *geoip.DB
}

// downloadClient fetches the databases: DB-IP City Lite is about 130 MB.
var downloadClient = &http.Client{Timeout: 30 * time.Minute}

type downloadState struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	URLs     []string    `json:"urls"`     // tried in order, passwords masked
	Loaded   *geoip.Info `json:"loaded"`   // null until a file is loaded
	Size     *int64      `json:"size"`     // of the file, null without one
	Modified *time.Time  `json:"modified"` // of the file
}

// listDownloads tells, for each database, where it comes from and what is
// in place.
func (s *server) listDownloads(w http.ResponseWriter, r *http.Request) {
	out := make([]downloadState, 0, len(s.Downloads))
	for _, d := range s.Downloads {
		st := downloadState{Name: d.Name, Path: d.DB.Path(), URLs: []string{}}
		for _, u := range geoip.URLs(d.URL) {
			st.URLs = append(st.URLs, redacted(u))
		}
		if d.DB.Loaded() {
			info := d.DB.Info()
			st.Loaded = &info
		}
		if fi, err := os.Stat(d.DB.Path()); err == nil {
			size, mod := fi.Size(), fi.ModTime().UTC()
			st.Size, st.Modified = &size, &mod
		}
		out = append(out, st)
	}
	render.JSON(w, r, out)
}

// download starts downloading a database over its file, then loads it.
func (s *server) download(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	for _, d := range s.Downloads {
		if d.Name != name {
			continue
		}
		params := map[string]any{"url": redacted(d.URL)}
		s.startTask(w, r, "download-"+name, params, true, func(ctx context.Context, progress func(jobs.Progress)) (any, error) {
			info, err := geoip.Download(ctx, downloadClient, d.URL, d.DB.Path(), d.ASN, func(done, total int64) {
				progress(jobs.Progress{Done: int(done), Total: int(max(total, 0))})
			})
			if err != nil {
				return nil, err
			}
			return info, d.DB.Reload()
		})
		return
	}
	render.Render(w, r, errorOf(http.StatusNotFound, "no such database"))
}

// redacted hides the password a URL may hold.
func redacted(s string) string {
	// Only then is the URL rebuilt, which would escape {year} and {month}.
	u, err := url.Parse(s)
	if err != nil || u.User == nil {
		return s
	}
	return u.Redacted()
}
