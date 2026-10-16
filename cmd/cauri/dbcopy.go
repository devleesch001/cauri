package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/dbcopy"
	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/store"
)

func init() {
	register(command{"db-copy", "copy the database into an empty one, SQLite or PostgreSQL (dry run without --yes)", runDBCopy})
}

const dbCopyUsage = `Usage:
  cauri db-copy --from-db-driver <d> --from-db-dsn <dsn> --to-db-driver <d> --to-db-dsn <dsn> [--yes]

Copies every row of a cauri database into another, of the same engine or
the other, ids included. The source is only read and must be at the schema
of this version (start the API on it once). The target must hold no rows;
it is migrated, then filled in one transaction: a failure leaves it empty.
Without --yes, only checks both and counts what would be copied.

Stop the API first: rows it writes during the copy would be missed. The
recordings stay in their directory (--tty-dir of the API). Then start the
API on the target (--db-driver, --db-dsn).

Options:`

func runDBCopy(ctx context.Context, args []string) error {
	fs := envflag.New("db-copy")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), dbCopyUsage)
		fs.PrintDefaults()
	}
	var from, to dbFlags
	fs.StringVar(&from.driver, "from-db-driver", store.SQLite, "source database: sqlite or postgres")
	fs.StringVar(&from.dsn, "from-db-dsn", "", "source: SQLite file path, or PostgreSQL URL")
	fs.StringVar(&to.driver, "to-db-driver", store.Postgres, "target database: sqlite or postgres")
	fs.StringVar(&to.dsn, "to-db-dsn", "", "target: SQLite file path, or PostgreSQL URL")
	yes := fs.Bool("yes", false, "copy; without it, only check and count")
	// The URLs may hold passwords, better kept out of the command line.
	if err := envflag.ParseOnly(fs, args, "from-db-driver", "from-db-dsn", "to-db-driver", "to-db-dsn"); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if from.dsn == "" || to.dsn == "" {
		fs.Usage()
		return errors.New("--from-db-dsn and --to-db-dsn are required")
	}
	// Opening a SQLite file creates it: a wrong source path would copy
	// an empty database, and a dry run must create nothing.
	if from.driver == store.SQLite && !exists(from.dsn) {
		return fmt.Errorf("source %s: no such file", from.dsn)
	}
	targetNew := to.driver == store.SQLite && !exists(to.dsn)

	log, err := newLogger("info")
	if err != nil {
		return err
	}
	log.Info("source database", "driver", from.driver, "location", from.location())
	src, err := store.Open(ctx, from.driver, from.dsn)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	defer func() { _ = src.Close() }()

	var counts []dbcopy.Count
	switch {
	case !*yes && targetNew:
		log.Info("target database does not exist yet", "driver", to.driver, "location", to.location())
		if err := store.CheckCurrent(ctx, src); err != nil {
			return fmt.Errorf("source: %w", err)
		}
		counts, err = dbcopy.Counts(ctx, src)
	default:
		var dst *bun.DB
		if dst, err = openTarget(ctx, &to, log.Info); err != nil {
			return err
		}
		defer func() { _ = dst.Close() }()
		if *yes {
			counts, err = dbcopy.Copy(ctx, src, dst, log, func(c dbcopy.Count) {
				if c.Rows%50000 == 0 {
					log.Info("copying", "table", c.Table, "rows", c.Rows)
				}
			})
		} else {
			counts, err = dbcopy.Check(ctx, src, dst)
		}
	}
	if err != nil {
		return err
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	for _, c := range counts {
		fmt.Fprintf(w, "%d\t %s\n", c.Rows, c.Table)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if *yes {
		fmt.Fprintln(os.Stderr, "Copied. Start the API with --db-driver "+to.driver+" and the target's --db-dsn.")
	} else {
		fmt.Fprintln(os.Stderr, "Dry run: nothing copied. Run again with --yes to copy.")
	}
	return nil
}

// openTarget opens the target, creating the directory of a SQLite file.
func openTarget(ctx context.Context, to *dbFlags, info func(string, ...any)) (*bun.DB, error) {
	if to.driver == store.SQLite && !strings.HasPrefix(to.dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(to.dsn), 0o750); err != nil {
			return nil, err
		}
	}
	info("target database", "driver", to.driver, "location", to.location())
	db, err := store.Open(ctx, to.driver, to.dsn)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	return db, nil
}

// exists reports whether a SQLite DSN names an existing file; a "file:"
// URI is left to SQLite.
func exists(dsn string) bool {
	if strings.HasPrefix(dsn, "file:") {
		return true
	}
	_, err := os.Stat(dsn)
	return !errors.Is(err, os.ErrNotExist)
}
