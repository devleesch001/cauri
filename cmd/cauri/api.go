package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"strings"
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
	adminPassword := fs.String("admin-password", "", "password of the administration pages; a random one is generated and logged at each start if neither it nor --admin-password-file is set")
	adminPasswordFile := fs.String("admin-password-file", "", "file holding the password of the administration pages, such as a container secret")
	trustedProxies := fs.String("trusted-proxies", "", "comma-separated addresses or CIDR ranges of reverse proxies whose X-Forwarded-For names the client, to limit password guesses per client; empty ignores the header")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}

	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	admin, err := adminPasswordOf(*adminPassword, *adminPasswordFile, log)
	if err != nil {
		return err
	}
	proxies, err := parsePrefixes(*trustedProxies)
	if err != nil {
		return fmt.Errorf("--trusted-proxies: %w", err)
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
		DB:             conn,
		Ingest:         svc,
		TTY:            tty,
		Tokens:         auth.NewTokens(conn),
		Admin:          auth.NewAdmin(admin),
		TrustedProxies: proxies,
		Logger:         log,
		Version:        buildVersion(),
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
		forgotten, err := svc.ForgetPartialTTYs(ctx)
		if err != nil && ctx.Err() == nil {
			log.Error("detaching partial recordings", "err", err)
		}
		if forgotten > 0 {
			log.Info("detached partial recordings of sessions with several channels", "sessions", forgotten)
		}
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

// adminPasswordOf returns the administration password set by the
// operator, or generates one and logs it: it then changes at each start.
func adminPasswordOf(password, file string, log *slog.Logger) (string, error) {
	switch {
	case password != "" && file != "":
		return "", errors.New("--admin-password and --admin-password-file: set only one")
	case file != "":
		b, err := os.ReadFile(file) //nolint:gosec // G304: path given by the operator
		if err != nil {
			return "", fmt.Errorf("--admin-password-file: %w", err)
		}
		password = strings.TrimSpace(string(b))
		if password == "" {
			return "", fmt.Errorf("--admin-password-file: %s is empty", file)
		}
		return password, nil
	case password != "":
		return password, nil
	}
	password = auth.GeneratePassword()
	log.Warn("administration password generated: it changes at each start, set CAURI_ADMIN_PASSWORD or CAURI_ADMIN_PASSWORD_FILE to keep one",
		"password", password)
	return password, nil
}

// parsePrefixes reads a comma-separated list of addresses and CIDR ranges.
func parsePrefixes(list string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(item)
		if err != nil {
			return nil, fmt.Errorf("%q: want an address or a CIDR range", item)
		}
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}
