// Package api is the HTTP layer of cauri: ingestion endpoints for the
// ingestors and read endpoints for the UI, all under /api.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"
	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// Authenticator resolves an ingestor token to its sensor.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (sensorID string, err error)
}

// Tokens are the ingestor tokens: checked on ingestion, managed from the
// administration pages.
type Tokens interface {
	Authenticator
	Create(ctx context.Context, sensor, name string) (store.IngestToken, string, error)
	List(ctx context.Context) ([]store.IngestToken, error)
	Revoke(ctx context.Context, id int64) error
}

// Config holds the dependencies of the HTTP handler.
type Config struct {
	DB     *bun.DB
	Ingest *ingest.Service
	TTY    *ttystore.Store // stored recordings, read for replays
	Tokens Tokens
	Logger *slog.Logger
	// Version is shown by GET /api/about.
	Version string
	// GeoIP is the GeoIP database, nil without one: its provider sets the
	// attribution the UI must show.
	GeoIP interface{ Info() geoip.Info }
	// ASN is the ASN database, nil without one; same attribution rules.
	ASN interface{ Info() geoip.Info }
	// Admin opens the administration routes (/api/admin) to the holder of
	// its password; nil leaves them out.
	Admin *auth.Admin
	// TrustedProxies are the reverse proxies whose X-Forwarded-For header
	// names the client, to limit password guesses per client; empty, the
	// header is ignored and the client is the connection's address.
	TrustedProxies []netip.Prefix
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
		// JSON whatever the Accept header: render would answer XML to
		// clients asking for it.
		r.Use(render.SetContentType(render.ContentTypeJSON))
		r.Get("/healthz", s.healthz)

		r.Group(func(r chi.Router) {
			r.Use(s.ingestAuth)
			r.Post(protocol.EventsPath[len("/api"):], s.postEvents)
			r.Put(protocol.TTYPathPrefix[len("/api"):]+"{session}", s.putTTY)
		})

		r.Group(func(r chi.Router) {
			r.Use(c.ReadAuth)
			r.Get("/sensors", s.listSensors)
			r.Get("/countries", s.listCountries)
			r.Get("/cities", s.listCities)
			r.Get("/stats/summary", s.getSummary)
			r.Get("/stats/activity", s.getActivity)
			r.Get("/stats/credentials", s.getCredentials)
			r.Get("/stats/payloads", s.getPayloads)
			r.Get("/stats/origins", s.getOrigins)
			r.Get("/stats/commands", s.getCommands)
			r.Get("/about", s.getAbout)
			r.Get("/attackers", s.listAttackers)
			r.Get("/attackers/{ip}", s.getAttacker)
			r.Get("/sessions", s.listSessions)
			r.Get("/sessions.csv", s.exportSessions)
			r.Get("/sessions/{id}", s.getSession)
			r.Get("/sessions/{id}/cast", s.getCast)
			r.Get("/sessions/{id}/chapters", s.getChapters)
			r.Get("/sessions/{id}/verdict", s.getVerdict)
			r.Get("/login-attempts", s.listLoginAttempts)
			r.Get("/login-attempts.csv", s.exportLoginAttempts)
		})

		if c.Admin != nil {
			r.Route("/admin", func(r chi.Router) {
				r.Use(clientIP(c.TrustedProxies))
				r.With(adminLoginLimiter()).Post("/login", s.adminLogin)
				r.Post("/logout", s.adminLogout)
				r.Group(func(r chi.Router) {
					r.Use(s.adminSession)
					r.Get("/session", s.adminCheck)
					r.Get("/tokens", s.listTokens)
					r.Post("/tokens", s.createToken)
					r.Post("/tokens/{id}/revoke", s.revokeToken)
				})
			})
		}

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			render.Render(w, r, errorOf(http.StatusNotFound, "not found"))
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			render.Render(w, r, errorOf(http.StatusMethodNotAllowed, "method not allowed"))
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

// clientIP resolves who sent a request (middleware.GetClientIP): the
// address X-Forwarded-For gives, read from the right past the trusted
// proxies, when the request comes from one of them; else the address of
// the connection.
func clientIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	if len(trusted) == 0 {
		return middleware.ClientIPFromRemoteAddr
	}
	prefixes := make([]string, len(trusted))
	for i, p := range trusted {
		prefixes[i] = p.String()
	}
	return middleware.ClientIPFromXFF(prefixes...)
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.PingContext(r.Context()); err != nil {
		s.Logger.Error("health check", "err", err)
		render.Render(w, r, errorOf(http.StatusServiceUnavailable, "database unavailable"))
		return
	}
	render.JSON(w, r, map[string]string{"status": "ok"})
}

// errResponse is an error answer: protocol.ErrorBody with its status.
type errResponse struct {
	protocol.ErrorBody
	status int
}

// Render sets the status of the answer (render.Renderer).
func (e *errResponse) Render(_ http.ResponseWriter, r *http.Request) error {
	render.Status(r, e.status)
	return nil
}

// errorOf returns the error answer of status, for render.Render.
func errorOf(status int, msg string) *errResponse {
	return &errResponse{ErrorBody: protocol.ErrorBody{Error: msg}, status: status}
}

// internalError logs err and answers 500 without leaking details.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	render.Render(w, r, errorOf(http.StatusInternalServerError, "internal error"))
}
