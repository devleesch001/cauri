package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"cauri/internal/store"
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
		SensorID:    q.str("sensor"),
		SrcIP:       q.str("ip"),
		Username:    q.str("username"),
		Password:    q.str("password"),
		Client:      q.str("client"),
		From:        q.time("from"),
		To:          q.time("to"),
		Login:       q.oneOf("login", store.LoginSuccess, store.LoginFailed),
		HasCommands: q.bool("has_commands"),
		HasTTY:      q.bool("has_tty"),
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

func (s *server) getSession(w http.ResponseWriter, r *http.Request) {
	d, err := store.GetSession(r.Context(), s.DB, chi.URLParam(r, "id"))
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
		SensorID:    q.str("sensor"),
		SrcIP:       q.str("ip"),
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
