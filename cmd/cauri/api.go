package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/ingestor"
	"github.com/devleesch001/cauri/internal/jobs"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/internal/verdict"
	"github.com/devleesch001/cauri/internal/version"
	"github.com/devleesch001/cauri/internal/webui"
)

func init() {
	register(command{"api", "run the API (with --ui, also serve the web UI)", runAPI})
	register(command{"all-in-one", "run the API and the web UI, reading Cowrie's directories itself (no ingestor)", runAllInOne})
}

func runAPI(ctx context.Context, args []string) error { return runServer(ctx, "api", args) }

func runAllInOne(ctx context.Context, args []string) error { return runServer(ctx, "all-in-one", args) }

// localIngestor are the options of the all-in-one mode's ingestor: the
// ingestor's own, but those reaching the API.
type localIngestor struct {
	sensor, logDir, ttyDir, stateDir, start *string
	batchSize                               *int
	flush, poll                             *time.Duration
}

func localIngestorFlags(fs *flag.FlagSet) localIngestor {
	return localIngestor{
		sensor:    fs.String("sensor-id", "", "identifier of this sensor, added to every event (required)"),
		logDir:    fs.String("cowrie-log-dir", "", "Cowrie var/log/cowrie directory, holding cowrie.json, only read (required)"),
		ttyDir:    fs.String("cowrie-tty-dir", "", "Cowrie var/lib/cowrie/tty directory, only read (required)"),
		stateDir:  fs.String("state-dir", "data/ingestor", "writable directory for the read position and pending recordings"),
		batchSize: fs.Int("batch-size", 500, "events per batch at most"),
		flush:     fs.Duration("flush-interval", time.Second, "delay before storing an incomplete batch"),
		poll:      fs.Duration("poll-interval", 500*time.Millisecond, "delay between checks for new lines"),
		start:     fs.String("start", "beginning", "without saved position: beginning (read existing logs) or end"),
	}
}

// runServer runs the API: alone (api), or, in the all-in-one mode, with
// the web UI and an ingestor reading Cowrie's directories in the same
// process, calling the ingestion service directly.
func runServer(ctx context.Context, name string, args []string) error {
	allInOne := name == "all-in-one"
	fs := envflag.New(name)
	var db dbFlags
	db.register(fs)
	listen := fs.String("listen", ":8080", "HTTP listen address")
	ttyDir := fs.String("tty-dir", "data/tty", "directory storing raw TTY recordings")
	maxTTY := fs.Int64("max-tty-size", ingest.DefaultMaxTTYSize, "largest TTY recording accepted, in bytes")
	withUI := new(true)
	var local localIngestor
	if allInOne {
		local = localIngestorFlags(fs)
	} else {
		withUI = fs.Bool("ui", false, "also serve the web UI, for a single container")
	}
	var geo geoFlags
	geo.register(fs)
	retention := defaultRetention
	fs.Var(&retention, "event-raw-data-retention-days", "days the raw Cowrie lines of events are kept, sessions and their details staying: -1, empty or null for ever, 0 none (not even stored)")
	adminPassword := fs.String("admin-password", "", "password of the administration pages; a random one is generated and logged at each start if neither it nor --admin-password-file is set")
	adminPasswordFile := fs.String("admin-password-file", "", "file holding the password of the administration pages, such as a container secret")
	trustedProxies := fs.String("trusted-proxies", "", "comma-separated addresses or CIDR ranges of reverse proxies whose X-Forwarded-For names the client, to limit password guesses per client; empty ignores the header")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}
	geo.defaults()
	if allInOne {
		switch {
		case *local.sensor == "", *local.logDir == "", *local.ttyDir == "":
			return errors.New("--sensor-id, --cowrie-log-dir and --cowrie-tty-dir are required")
		case *local.start != "beginning" && *local.start != "end":
			return fmt.Errorf("--start: want beginning or end, got %q", *local.start)
		}
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
	conn, err := db.open(ctx, log)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	tty, err := ttystore.New(*ttyDir)
	if err != nil {
		return err
	}

	svc := ingest.New(conn, tty, *maxTTY)
	// The administration's tasks stop with the process, between two batches
	// of a purge; the database closes after them.
	tasks := jobs.New(ctx)
	defer tasks.Wait()
	cfg := api.Config{
		DB:             conn,
		Ingest:         svc,
		TTY:            tty,
		Tokens:         auth.NewTokens(conn),
		Admin:          auth.NewAdmin(admin),
		TrustedProxies: proxies,
		Logger:         log,
		Version:        version.Get(),
		Jobs:           tasks,
		Options:        maskSecrets(envflag.Describe(fs)),
		// One image runs every role: its environment holds the
		// ingestor's variables too (CAURI_STATE_DIR).
		UnknownEnv: envflag.Unknown(fs, ingestorFlagSet()),
	}
	if geo.db != "" {
		loc, err := geoip.Open(geo.db, log)
		if err != nil {
			return err
		}
		defer func() { _ = loc.Close() }()
		svc.SetLocator(loc)
		cfg.GeoIP = loc
		cfg.Downloads = append(cfg.Downloads, api.Download{Name: "geoip", URL: geo.url, DB: loc})
		// Sessions stored before the database was loaded: at start, and
		// whenever a file appears or replaces it, copied or downloaded.
		var mu sync.Mutex
		locate := func() {
			mu.Lock()
			defer mu.Unlock()
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
		}
		loc.OnLoad(locate)
		if loc.Loaded() {
			go locate()
		}
		go loc.Watch(ctx, time.Minute)
	}
	switch retention {
	case keepForever:
		log.Info("raw lines kept for ever")
	case 0:
		svc.SetRawData(false)
		log.Info("raw lines not stored; those stored before are deleted")
		go purgeRaw(ctx, conn, 0, log)
	default:
		log.Info("raw lines kept", "days", int(retention))
		go purgeRaw(ctx, conn, time.Duration(retention)*24*time.Hour, log)
	}
	if geo.asnDB != "" {
		asn, err := geoip.Open(geo.asnDB, log)
		if err != nil {
			return err
		}
		defer func() { _ = asn.Close() }()
		svc.SetNetworker(asn)
		cfg.ASN = asn
		cfg.Downloads = append(cfg.Downloads, api.Download{Name: "asn", URL: geo.asnURL, ASN: true, DB: asn})
		// Sessions stored before the database was loaded, as above.
		var mu sync.Mutex
		network := func() {
			mu.Lock()
			defer mu.Unlock()
			n, err := svc.NetworkSessions(ctx)
			if err != nil && ctx.Err() == nil {
				log.Error("finding the network of stored sessions", "err", err)
			}
			if n > 0 {
				log.Info("found the network of stored sessions", "sessions", n)
			}
		}
		asn.OnLoad(network)
		if asn.Loaded() {
			go network()
		}
		go asn.Watch(ctx, time.Minute)
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
	if !allInOne {
		log.Info("starting api", "version", version.Get().String(), "db", db.driver, "ui", *withUI)
		return serve(ctx, srv, log)
	}

	// The ingestor stops the server when it fails, and the other way round.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ingested := make(chan error, 1)
	go func() {
		defer cancel()
		ingested <- ingestor.Run(ctx, ingestor.Config{
			SensorID:      *local.sensor,
			LogDir:        *local.logDir,
			TTYDir:        *local.ttyDir,
			StateDir:      *local.stateDir,
			Sink:          ingestor.NewLocalSink(svc),
			BatchSize:     *local.batchSize,
			FlushInterval: *local.flush,
			PollInterval:  *local.poll,
			FromStart:     *local.start == "beginning",
			Logger:        log,
		})
	}()
	log.Info("starting all-in-one", "version", version.Get().String(), "db", db.driver, "sensor", *local.sensor,
		"cowrie_log_dir", *local.logDir, "cowrie_tty_dir", *local.ttyDir)
	err = serve(ctx, srv, log)
	cancel()
	if ierr := <-ingested; ierr != nil && !errors.Is(ierr, context.Canceled) {
		return fmt.Errorf("ingestor: %w", ierr)
	}
	return err
}

// maskSecrets hides what the administration page must not show: the
// administration password, and the password in a database URL. The path
// of a password file stays: it is no secret, its content is.
func maskSecrets(opts []envflag.Option) []envflag.Option {
	for i, o := range opts {
		switch o.Name {
		case "admin-password":
			if o.Value != "" {
				opts[i].Value = "****"
			}
		case "db-dsn", "geoip-db-url", "geoip-asn-db-url":
			opts[i].Value = maskDSN(o.Value)
		}
	}
	return opts
}

// maskDSN hides the password of a database URL (postgres://u:p@h/db) or of
// key=value settings (password=p).
func maskDSN(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if _, ok := u.User.Password(); ok {
			u.User = url.UserPassword(u.User.Username(), "****")
			return strings.Replace(u.String(), "%2A%2A%2A%2A", "****", 1)
		}
		return dsn
	}
	return passwordSetting.ReplaceAllString(dsn, "${1}****")
}

// passwordSetting is the password of key=value database settings.
var passwordSetting = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('[^']*'|\S+)`)

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
