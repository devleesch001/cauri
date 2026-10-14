// Package api is the HTTP layer of cauri: ingestion endpoints for the
// ingestors and read endpoints for the UI, all under /api.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// Authenticator resolves an ingestor token to its sensor.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (sensorID string, err error)
}

// Config holds the dependencies of the HTTP handler.
type Config struct {
	DB     *bun.DB
	Ingest *ingest.Service
	TTY    *ttystore.Store // stored recordings, read for replays
	Tokens Authenticator
	Logger *slog.Logger
	// ReadAuth protects the read endpoints. Nil lets every request through;
	// UI authentication will plug in here.
	ReadAuth func(http.Handler) http.Handler
	// UI, if set, serves every path outside /api.
	UI http.Handler
}

// New returns the HTTP handler of the API.
func New(c Config) http.Handler {
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.ReadAuth == nil {
		c.ReadAuth = func(next http.Handler) http.Handler { return next }
	}
	s := &server{Config: c}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer, s.logRequests)

	r.Route("/api", func(r chi.Router) {
		r.Get("/healthz", s.healthz)

		r.Group(func(r chi.Router) {
			r.Use(s.ingestAuth)
			r.Post(protocol.EventsPath[len("/api"):], s.postEvents)
			r.Put(protocol.TTYPathPrefix[len("/api"):]+"{session}", s.putTTY)
		})

		r.Group(func(r chi.Router) {
			r.Use(c.ReadAuth)
			r.Get("/sensors", s.listSensors)
			r.Get("/sessions", s.listSessions)
			r.Get("/sessions/{id}", s.getSession)
			r.Get("/sessions/{id}/cast", s.getCast)
			r.Get("/sessions/{id}/chapters", s.getChapters)
			r.Get("/sessions/{id}/verdict", s.getVerdict)
			r.Get("/login-attempts", s.listLoginAttempts)
		})

		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		})
	})
	if c.UI != nil {
		r.Handle("/*", c.UI)
	}
	return r
}

type server struct {
	Config
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.PingContext(r.Context()); err != nil {
		s.Logger.Error("health check", "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, protocol.ErrorBody{Error: msg})
}

// internalError logs err and answers 500 without leaking details.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
