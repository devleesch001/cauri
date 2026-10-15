package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/store"
)

// list is the envelope of paginated responses.
type list[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func (s *server) listSensors(w http.ResponseWriter, r *http.Request) {
	sensors, err := store.ListSensors(r.Context(), s.DB)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sensors)
}

func (s *server) listSessions(w http.ResponseWriter, r *http.Request) {
	q := query{v: r.URL.Query()}
	f := store.SessionFilter{
		Sensor:      q.str("sensor"),
		SrcIP:       q.str("ip"),
		Country:     q.country("country"),
		City:        q.str("city"),
		Username:    q.str("username"),
		Password:    q.str("password"),
		Client:      q.str("client"),
		From:        q.time("from"),
		To:          q.time("to"),
		Login:       q.oneOf("login", store.LoginSuccess, store.LoginFailed),
		Verdict:     q.oneOf("verdict", store.VerdictHuman, store.VerdictBot),
		HasCommands: q.bool("has_commands"),
		HasTTY:      q.bool("has_tty"),
		File:        q.str("file"),
		ASN:         int64(q.int("asn")),
		Command:     q.str("command"),
		HASSH:       q.str("hassh"),
		Page:        q.page(),
	}
	if q.err != nil {
		writeError(w, http.StatusBadRequest, q.err.Error())
		return
	}
	rows, total, err := store.ListSessions(r.Context(), s.DB, f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newList(rows, total, f.Page))
}

// sessionID returns the session id of the URL. An invalid one is 0,
// which no session has: the same "not found" as an unknown id.
func sessionID(r *http.Request) int64 {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	d, err := store.GetSession(r.Context(), s.DB, sessionID(r))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *server) listLoginAttempts(w http.ResponseWriter, r *http.Request) {
	q := query{v: r.URL.Query()}
	f := store.LoginAttemptFilter{
		Sensor:      q.str("sensor"),
		SrcIP:       q.str("ip"),
		Country:     q.country("country"),
		Username:    q.str("username"),
		Password:    q.str("password"),
		From:        q.time("from"),
		To:          q.time("to"),
		Success:     q.bool("success"),
		Interactive: q.bool("interactive"),
		Page:        q.page(),
	}
	if q.err != nil {
		writeError(w, http.StatusBadRequest, q.err.Error())
		return
	}
	rows, total, err := store.ListLoginAttempts(r.Context(), s.DB, f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newList(rows, total, f.Page))
}

func newList[T any](items []T, total int, p store.Page) list[T] {
	limit := p.Limit
	if limit <= 0 {
		limit = store.DefaultLimit
	}
	return list[T]{Items: items, Total: total, Limit: min(limit, store.MaxLimit), Offset: p.Offset}
}

// query parses query parameters, keeping the first error.
type query struct {
	v   url.Values
	err error
}

func (q *query) str(name string) string { return q.v.Get(name) }

func (q *query) fail(name, want string) {
	if q.err == nil {
		q.err = fmt.Errorf("parameter %s: want %s", name, want)
	}
}

// time accepts RFC 3339 timestamps.
func (q *query) time(name string) *time.Time {
	s := q.v.Get(name)
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		q.fail(name, "an RFC 3339 timestamp")
		return nil
	}
	return &t
}

func (q *query) bool(name string) *bool {
	s := q.v.Get(name)
	if s == "" {
		return nil
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		q.fail(name, "true or false")
		return nil
	}
	return &b
}

// country accepts an ISO 3166-1 alpha-2 code, in any case.
func (q *query) country(name string) string {
	s := strings.ToUpper(q.v.Get(name))
	if s == "" {
		return ""
	}
	if len(s) != 2 || s[0] < 'A' || s[0] > 'Z' || s[1] < 'A' || s[1] > 'Z' {
		q.fail(name, "a two-letter country code")
		return ""
	}
	return s
}

func (q *query) oneOf(name string, values ...string) string {
	s := q.v.Get(name)
	if s == "" {
		return ""
	}
	for _, v := range values {
		if s == v {
			return s
		}
	}
	q.fail(name, fmt.Sprintf("one of %v", values))
	return ""
}

func (q *query) int(name string) int {
	s := q.v.Get(name)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		q.fail(name, "a positive integer")
		return 0
	}
	return n
}

func (q *query) page() store.Page {
	return store.Page{Limit: q.int("limit"), Offset: q.int("offset")}
}

func (s *server) listCountries(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.ListCountries)
}

func (s *server) listCities(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.ListCities)
}

func (s *server) getSummary(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.Summarize)
}

func (s *server) getActivity(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.GetActivity)
}

func (s *server) getCredentials(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.GetCredentials)
}

func (s *server) getPayloads(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.GetPayloads)
}

func (s *server) getCommands(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.GetCommands)
}

func (s *server) getOrigins(w http.ResponseWriter, r *http.Request) {
	serveStats(s, w, r, store.GetOrigins)
}

// serveStats answers a statistic of the sessions selected by the filters
// that apply to statistics: sensor, period, human or bot.
func serveStats[T any](s *server, w http.ResponseWriter, r *http.Request, get func(context.Context, bun.IDB, store.StatsFilter) (T, error)) {
	q := query{v: r.URL.Query()}
	f := store.StatsFilter{
		Sensor:  q.str("sensor"),
		From:    q.time("from"),
		To:      q.time("to"),
		Verdict: q.oneOf("verdict", store.VerdictHuman, store.VerdictBot),
	}
	if q.err != nil {
		writeError(w, http.StatusBadRequest, q.err.Error())
		return
	}
	out, err := get(r.Context(), s.DB, f)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type about struct {
	Version string      `json:"version"`
	GeoIP   *geoip.Info `json:"geoip"` // null without a GeoIP database
	ASN     *geoip.Info `json:"asn"`   // null without an ASN database
}

// getAbout describes this cauri: the UI shows the attribution the GeoIP
// database requires.
func (s *server) getAbout(w http.ResponseWriter, _ *http.Request) {
	out := about{Version: s.Version}
	if s.GeoIP != nil {
		info := s.GeoIP.Info()
		out.GeoIP = &info
	}
	if s.ASN != nil {
		info := s.ASN.Info()
		out.ASN = &info
	}
	writeJSON(w, http.StatusOK, out)
}
