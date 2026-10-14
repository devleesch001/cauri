package main

import (
	"context"
	"net/http"
	"time"

	"cauri/internal/api"
	"cauri/internal/auth"
	"cauri/internal/envflag"
	"cauri/internal/ingest"
	"cauri/internal/ttystore"
)

func init() {
	register(command{"api", "run the API (with --ui, also serve the web UI)", runAPI})
}

func runAPI(ctx context.Context, args []string) error {
	fs := envflag.New("api")
	var db dbFlags
	db.register(fs)
	listen := fs.String("listen", ":8080", "HTTP listen address")
	ttyDir := fs.String("tty-dir", "data/tty", "directory storing raw TTY recordings")
	maxTTY := fs.Int64("max-tty-size", ingest.DefaultMaxTTYSize, "largest TTY recording accepted, in bytes")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}

	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	conn, err := db.open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	tty, err := ttystore.New(*ttyDir)
	if err != nil {
		return err
	}

	handler := api.New(api.Config{
		DB:     conn,
		Ingest: ingest.New(conn, tty, *maxTTY),
		Tokens: auth.NewTokens(conn),
		Logger: log,
	})
	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // TTY uploads
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	log.Info("starting api", "version", buildVersion(), "db", db.driver)
	return serve(ctx, srv, log)
}
