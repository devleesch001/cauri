package api

import (
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// The administration pages: one password opens a session, kept in a
// cookie, that manages the ingestor tokens.

const (
	adminCookie = "cauri_admin"
	adminPath   = "/api/admin"
	// AdminHeader must be sent with every request that changes something:
	// a page of another site cannot set it, so it cannot act for the
	// administrator (on top of the SameSite cookie).
	AdminHeader = "X-Cauri-Admin"
)

// failedLoginDelay slows guessing the password, per client (see
// clientOf): its attempts are checked one at a time, and a wrong one
// blocks it this long. Attempts arriving meanwhile are refused at once,
// not queued: at most one guess per delay and client, without piling
// requests up, and other clients can still sign in.
const failedLoginDelay = time.Second

// maxGuessers is the number of clients past which those no longer
// blocked are forgotten.
const maxGuessers = 1024

type adminState struct {
	mu sync.Mutex
	// Clients with an attempt being checked (zero time) or blocked until
	// the time given.
	busy map[netip.Prefix]time.Time
}

// take reserves the turn of client c, false if it is checking or blocked.
func (a *adminState) take(c netip.Prefix, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if until, ok := a.busy[c]; ok && (until.IsZero() || now.Before(until)) {
		return false
	}
	if a.busy == nil {
		a.busy = map[netip.Prefix]time.Time{}
	}
	if len(a.busy) >= maxGuessers {
		for k, until := range a.busy {
			if !until.IsZero() && !now.Before(until) {
				delete(a.busy, k)
			}
		}
	}
	a.busy[c] = time.Time{}
	return true
}

// done ends the turn of c: a wrong guess blocks it for failedLoginDelay.
func (a *adminState) done(c netip.Prefix, wrong bool, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if wrong {
		a.busy[c] = now.Add(failedLoginDelay)
	} else {
		delete(a.busy, c)
	}
}

// adminSession refuses requests without a valid session, and changes
// without the administration header.
func (s *server) adminSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.adminChange(w, r) {
			return
		}
		c, err := r.Cookie(adminCookie)
		if err != nil || !s.Admin.ValidSession(c.Value) {
			render.Render(w, r, errorOf(http.StatusUnauthorized, "not signed in"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// adminChange checks the administration header on requests that change
// something, answering 403 without it.
func (s *server) adminChange(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(AdminHeader) == "" {
		render.Render(w, r, errorOf(http.StatusForbidden, "missing "+AdminHeader+" header"))
		return false
	}
	return true
}

func (s *server) adminLogin(w http.ResponseWriter, r *http.Request) {
	if !s.adminChange(w, r) {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if err := render.DecodeJSON(http.MaxBytesReader(w, r.Body, 4096), &in); err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, "want a JSON body with a password"))
		return
	}
	// The turn is taken before checking: checked first, a burst of guesses
	// would all be answered, the right one at once.
	client := s.clientOf(r)
	if !s.admin.take(client, time.Now()) {
		w.Header().Set("Retry-After", "1")
		render.Render(w, r, errorOf(http.StatusTooManyRequests, "too many sign-in attempts, retry in a second"))
		return
	}
	ok := s.Admin.Check(in.Password)
	s.admin.done(client, !ok, time.Now())
	if !ok {
		s.Logger.Warn("failed administration login", "client", client.String(), "remote", r.RemoteAddr)
		render.Render(w, r, errorOf(http.StatusUnauthorized, "wrong password"))
		return
	}
	value, exp := s.Admin.NewSession()
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Value: value, Path: adminPath, Expires: exp,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) adminLogout(w http.ResponseWriter, r *http.Request) {
	if !s.adminChange(w, r) {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: adminCookie, Path: adminPath, MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// adminCheck answers 204 to a signed-in administrator: the UI asks it to
// choose between the sign-in form and the pages.
func (s *server) adminCheck(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// adminToken is an ingestor token as the administration pages show it:
// never its hash.
type adminToken struct {
	ID         int64       `json:"id"`
	Sensor     string      `json:"sensor"`
	Name       string      `json:"name"`
	CreatedAt  store.Time  `json:"created_at"`
	LastUsedAt *store.Time `json:"last_used_at"`
	RevokedAt  *store.Time `json:"revoked_at"`
}

func newAdminToken(t store.IngestToken) adminToken {
	return adminToken{t.ID, t.Sensor, t.Name, t.CreatedAt, t.LastUsedAt, t.RevokedAt}
}

func (s *server) listTokens(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Tokens.List(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]adminToken, 0, len(recs))
	for _, t := range recs {
		out = append(out, newAdminToken(t))
	}
	render.JSON(w, r, out)
}

func (s *server) createToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sensor string `json:"sensor"`
		Name   string `json:"name"`
	}
	if err := render.DecodeJSON(http.MaxBytesReader(w, r.Body, 4096), &in); err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, "want a JSON body with a sensor and a name"))
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !protocol.ValidSensorID(in.Sensor) {
		render.Render(w, r, errorOf(http.StatusBadRequest, "invalid sensor id: use 1 to 64 letters, digits, '.', '_' or '-'"))
		return
	}
	if len(in.Name) > 200 {
		render.Render(w, r, errorOf(http.StatusBadRequest, "name: 200 characters at most"))
		return
	}
	rec, secret, err := s.Tokens.Create(r.Context(), in.Sensor, in.Name)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Logger.Info("ingestor token created", "id", rec.ID, "sensor", rec.Sensor)
	render.Status(r, http.StatusCreated)
	render.JSON(w, r, struct {
		Token  adminToken `json:"token"`
		Secret string     `json:"secret"` // shown once, never stored
	}{newAdminToken(rec), secret})
}

func (s *server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		render.Render(w, r, errorOf(http.StatusNotFound, "token not found"))
		return
	}
	err = s.Tokens.Revoke(r.Context(), id)
	if errors.Is(err, auth.ErrNotFound) {
		render.Render(w, r, errorOf(http.StatusNotFound, "token not found"))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Logger.Info("ingestor token revoked", "id", id)
	w.WriteHeader(http.StatusNoContent)
}
