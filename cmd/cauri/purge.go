package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/purge"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
)

func init() {
	register(command{"purge", "delete sessions and everything stored about them (dry run without --yes)", runPurge})
}

const purgeUsage = `Usage:
  cauri purge <criteria> [--yes]

Deletes the sessions matching every criterion given, with their events,
raw lines, login attempts, commands, transfers, and the recordings no other
session has (rows and files). Without --yes, only says what would go.

Lines Cowrie still has come back if an ingestor sends them again (a new
ingestor started from the beginning, a lost state file).

Criteria are exact values and are never read from the environment.
Database options (--db-driver, --db-dsn) and --tty-dir are the same as for
"cauri api", environment variables included.

Options:`

func runPurge(ctx context.Context, args []string) error {
	fs := envflag.New("purge")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), purgeUsage)
		fs.PrintDefaults()
	}
	var db dbFlags
	db.register(fs)
	ttyDir := fs.String("tty-dir", "data/tty", "directory storing raw TTY recordings")
	var f store.SessionFilter
	fs.StringVar(&f.SrcIP, "ip", "", "source address")
	fs.StringVar(&f.Session, "session", "", "Cowrie session identifier")
	fs.StringVar(&f.Username, "username", "", "username tried in the session")
	fs.StringVar(&f.Password, "password", "", "password tried in the session")
	fs.StringVar(&f.Command, "command", "", "command line run in the session")
	fs.StringVar(&f.HASSH, "hassh", "", "client fingerprint")
	fs.StringVar(&f.File, "file", "", "SHA-256 of a file the session transferred")
	fs.StringVar(&f.Sensor, "sensor", "", "sensor identifier")
	from := fs.String("from", "", "sessions started at or after this time (2026-10-16 is midnight UTC, or RFC 3339)")
	to := fs.String("to", "", "sessions started before this time, same formats")
	yes := fs.Bool("yes", false, "delete; without it, only count")
	if err := envflag.ParseOnly(fs, args, "db-driver", "db-dsn", "tty-dir"); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	var err error
	if f.From, err = parseWhen(*from); err != nil {
		return fmt.Errorf("--from: %w", err)
	}
	if f.To, err = parseWhen(*to); err != nil {
		return fmt.Errorf("--to: %w", err)
	}
	// New would create a missing directory: a wrong path would leave every
	// file behind, silently.
	if fi, err := os.Stat(*ttyDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("--tty-dir %s: not a directory of recordings", *ttyDir)
	}

	log, err := newLogger("info")
	if err != nil {
		return err
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

	c, err := purge.New(conn, tty).Sessions(ctx, f, *yes)
	if errors.Is(err, purge.ErrNoCriteria) {
		fs.Usage()
		return flag.ErrHelp
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	for _, r := range []struct {
		n    int
		name string
	}{
		{c.Sessions, "sessions"}, {c.Events, "events"}, {c.LoginAttempts, "login attempts"},
		{c.Commands, "commands"}, {c.Transfers, "transfers"}, {c.Recordings, "recordings"},
	} {
		fmt.Fprintf(w, "%d\t %s\n", r.n, r.name)
	}
	if ferr := w.Flush(); err == nil {
		err = ferr
	}
	switch {
	case err != nil:
	case c.Sessions == 0:
		fmt.Fprintln(os.Stderr, "No session matches.")
	case !*yes:
		fmt.Fprintln(os.Stderr, "Dry run: nothing deleted. Run again with --yes to delete.")
	default:
		fmt.Fprintln(os.Stderr, "Deleted.")
	}
	return err
}

// parseWhen reads a day (midnight UTC) or an RFC 3339 time; empty is none.
func parseWhen(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		if t, err = time.Parse(time.RFC3339Nano, s); err != nil {
			return nil, fmt.Errorf("%q: want 2026-10-16 or 2026-10-16T09:00:00Z", s)
		}
	}
	return &t, nil
}
