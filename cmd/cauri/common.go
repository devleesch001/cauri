package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
)

// dbFlags are the database options shared by api and token.
type dbFlags struct {
	driver string
	dsn    string
}

func (f *dbFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.driver, "db-driver", store.SQLite, "database: sqlite or postgres")
	fs.StringVar(&f.dsn, "db-dsn", "data/cauri.db", "SQLite file path, or PostgreSQL URL (postgres://user:pass@host/db)")
}

// location says where the database is, for the log: the file of a SQLite
// database, the host and name of a PostgreSQL one, never its credentials.
func (f *dbFlags) location() string {
	if f.driver == store.SQLite {
		if abs, err := filepath.Abs(strings.TrimPrefix(f.dsn, "file:")); err == nil && !strings.Contains(f.dsn, "?") {
			return abs
		}
		return f.dsn
	}
	u, err := url.Parse(f.dsn)
	if err != nil || u.Host == "" {
		return "(unreadable url)"
	}
	return u.Host + u.Path
}

// open connects to the database, says in log what it is and what state its
// schema is in, and applies the pending migrations before returning. The
// parent directory of a SQLite file is created if needed.
func (f *dbFlags) open(ctx context.Context, log *slog.Logger) (*bun.DB, error) {
	if f.driver == store.SQLite && !strings.HasPrefix(f.dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(f.dsn), 0o750); err != nil {
			return nil, err
		}
	}
	log.Info("opening the database", "driver", f.driver, "location", f.location())
	db, err := store.Open(ctx, f.driver, f.dsn)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, db, log); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// newLogger returns a JSON logger at the given level ("debug", "info"...).
func newLogger(level string) (*slog.Logger, error) {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("log level: %w", err)
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l})), nil
}

// serve runs srv until ctx is done, then shuts it down gracefully.
func serve(ctx context.Context, srv *http.Server, log *slog.Logger) error {
	// Listen first, so that "listening" is only logged once the port is ours.
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
	if err != nil {
		return err
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	log.Info("listening", "addr", ln.Addr().String())
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
