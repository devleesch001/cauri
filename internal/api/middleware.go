package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/devleesch001/cauri/internal/auth"
)

type ctxKey int

const sensorKey ctxKey = iota

// sensorFrom returns the sensor authenticated by ingestAuth.
func sensorFrom(ctx context.Context) string {
	s, _ := ctx.Value(sensorKey).(string)
	return s
}

// ingestAuth authenticates an ingestor by its bearer token.
func (s *server) ingestAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			s.refuse(w, r, http.StatusUnauthorized, "missing bearer token", "remote", r.RemoteAddr)
			return
		}
		sensor, err := s.Tokens.Authenticate(r.Context(), token)
		if errors.Is(err, auth.ErrUnauthorized) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			s.refuse(w, r, http.StatusUnauthorized, err.Error(), "remote", r.RemoteAddr)
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sensorKey, sensor)))
	})
}

// logRequests logs every request: successful ones at debug level, since
// ingestors post every second.
func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		level := slog.LevelDebug
		switch {
		case ww.Status() >= 500:
			level = slog.LevelError
		case ww.Status() >= 400:
			level = slog.LevelInfo
		}
		s.Logger.Log(r.Context(), level, "http request",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "duration", time.Since(start).String(), "remote", r.RemoteAddr)
	})
}
