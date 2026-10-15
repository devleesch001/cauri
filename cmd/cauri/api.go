package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/internal/verdict"
	"github.com/devleesch001/cauri/internal/webui"
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
	withUI := fs.Bool("ui", false, "also serve the web UI, for a single container")
	geoDB := fs.String("geoip-db", "", "MMDB GeoIP database (DB-IP Lite or MaxMind GeoLite2) to locate sessions; none by default")
	asnDB := fs.String("geoip-asn-db", "", "MMDB ASN database (DB-IP ASN Lite or MaxMind GeoLite2-ASN) to find the network of sessions; none by default")
	retention := fs.Int("raw-retention-days", 0, "delete the raw Cowrie lines of events older than this many days, keeping sessions and their details; 0 keeps them")
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

	svc := ingest.New(conn, tty, *maxTTY)
	cfg := api.Config{
		DB:      conn,
		Ingest:  svc,
		TTY:     tty,
		Tokens:  auth.NewTokens(conn),
		Logger:  log,
		Version: buildVersion(),
	}
	if *geoDB != "" {
		geo, err := geoip.Open(*geoDB, log)
		if err != nil {
			return err
		}
		defer func() { _ = geo.Close() }()
		svc.SetLocator(geo)
		cfg.GeoIP = geo
		// The operator replaces the file to update it.
		go geo.Watch(ctx, time.Minute)
		// Sessions stored before the database was configured.
		go func() {
			n, err := svc.LocateSessions(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error("locating stored sessions", "err", err)
			}
			if n > 0 {
				log.Info("located stored sessions", "sessions", n)
			}
			// Then the coordinates of sessions located before they were stored.
			n, err = svc.PlaceSessions(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error("placing stored sessions", "err", err)
			}
			if n > 0 {
				log.Info("placed stored sessions", "sessions", n)
			}
		}()
	}
	if *retention < 0 {
		return fmt.Errorf("--raw-retention-days: want 0 or more days")
	}
	if *retention > 0 {
		go purgeRaw(ctx, conn, time.Duration(*retention)*24*time.Hour, log)
	}
	if *asnDB != "" {
		asn, err := geoip.Open(*asnDB, log)
		if err != nil {
			return err
		}
		defer func() { _ = asn.Close() }()
		svc.SetNetworker(asn)
		cfg.ASN = asn
		go asn.Watch(ctx, time.Minute)
		// Sessions stored before the database was configured.
		go func() {
			n, err := svc.NetworkSessions(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error("finding the network of stored sessions", "err", err)
			}
			if n > 0 {
				log.Info("found the network of stored sessions", "sessions", n)
			}
		}()
	}
	// Sessions judged by older criteria, or before verdicts were stored.
	go func() {
		n, err := svc.JudgeSessions(ctx)
		if err != nil && ctx.Err() == nil {
			log.Error("judging stored sessions", "err", err)
		}
		if n > 0 {
			log.Info("judged stored sessions", "sessions", n, "criteria", verdict.Version)
		}
	}()
	if *withUI {
		cfg.UI = webui.Handler()
	}
	handler := api.New(cfg)
	srv := &http.Server{
		Addr:              *listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // TTY uploads
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	log.Info("starting api", "version", buildVersion(), "db", db.driver, "ui", *withUI)
	return serve(ctx, srv, log)
}

// purgeRaw deletes, at start then every hour until ctx is done, the raw
// Cowrie lines older than keep.
func purgeRaw(ctx context.Context, db *bun.DB, keep time.Duration, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		n, err := store.PurgeRawEvents(ctx, db, time.Now().Add(-keep))
		if err != nil && ctx.Err() == nil {
			log.Error("purging raw events", "err", err)
		}
		if n > 0 {
			log.Info("purged raw events", "events", n, "older_than", keep.String())
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
