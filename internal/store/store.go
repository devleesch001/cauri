// Package store opens the database (SQLite or PostgreSQL), applies the
// embedded migrations of its engine and defines the bun models.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"github.com/uptrace/bun/migrate"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Supported values of the driver option.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// migrations holds one directory per engine: the same schema, with the
// types of each (see docs/db.md).
//
//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrations embed.FS

// sqlitePragmas are appended to every SQLite DSN: foreign keys on, WAL so
// readers do not block the writer, a busy timeout for concurrent writers
// (such as "cauri token"), and immediate transactions to avoid lock upgrades.
const sqlitePragmas = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Open connects to the database and checks the connection.
//
// For SQLite, dsn is a file path or a "file:" URI; for PostgreSQL, a
// "postgres://" URL.
func Open(ctx context.Context, driver, dsn string) (*bun.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("empty database dsn")
	}
	var db *bun.DB
	switch driver {
	case SQLite:
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		sqldb, err := sql.Open("sqlite", dsn+sep+sqlitePragmas)
		if err != nil {
			return nil, err
		}
		db = bun.NewDB(sqldb, sqlitedialect.New())
	case Postgres:
		sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(dsn)))
		db = bun.NewDB(sqldb, pgdialect.New())
	default:
		return nil, fmt.Errorf("unknown database driver %q (want %s or %s)", driver, SQLite, Postgres)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to %s: %w", driver, err)
	}
	return db, nil
}

// ErrOldSchema means the database was created by cauri v0.0.x, whose
// schema the current one replaces without conversion.
var ErrOldSchema = errors.New("database created by cauri v0.0.x: its schema is not supported, start from an empty database and re-ingest (see README)")

// ErrNewerSchema means the database was migrated by a newer cauri than this
// one: running on it could misread or damage its data.
var ErrNewerSchema = errors.New("database schema is newer than this version of cauri")

// Migrate reports the state of the database in log, then applies the pending
// migrations of the engine of db, logging each: its name, then how long it
// took. It returns once the schema is current, or on the first error. A nil
// log says nothing.
func Migrate(ctx context.Context, db *bun.DB, log *slog.Logger) error {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	engine := SQLite
	if db.Dialect().Name().String() == "pg" {
		engine = Postgres
	}
	if old, err := hasTable(ctx, db, engine, "goose_db_version"); err != nil {
		return err
	} else if old {
		return ErrOldSchema
	}
	log.Info("database", engineAttrs(ctx, db, engine)...)

	found, err := migrationsOf(engine)
	if err != nil {
		return err
	}
	// Each migration says when it starts and ends.
	ms := migrate.NewMigrations()
	for _, mig := range found.Sorted() {
		up := mig.Up
		name := mig.Name + "_" + mig.Comment
		mig.Up = func(ctx context.Context, m *migrate.Migrator, mg *migrate.Migration) error {
			start := time.Now()
			log.Info("applying migration", "migration", name)
			if err := up(ctx, m, mg); err != nil {
				return err
			}
			log.Info("migration applied", "migration", name, "duration_ms", time.Since(start).Milliseconds())
			return nil
		}
		ms.Add(mig)
	}
	// Marked applied only once run: by default bun marks a migration first,
	// so a failed one would count as done. No Lock: bun's lock is a row
	// left behind if the API dies while migrating, blocking every start;
	// one API runs per database.
	m := migrate.NewMigrator(db, ms, migrate.WithMarkAppliedOnSuccess(true))
	if err := m.Init(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	all, err := m.MigrationsWithStatus(ctx)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if missing, err := m.MissingMigrations(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	} else if len(missing) > 0 {
		log.Error("database schema is newer than this version", "unknown_migrations", names(missing))
		return fmt.Errorf("%w: it has migration %s", ErrNewerSchema, missing[0].Name)
	}
	pending := all.Unapplied()
	// Names are numbered, so the greatest is the newest.
	var from, to string
	for _, mig := range all {
		if mig.IsApplied() {
			from = max(from, label(mig))
		}
		to = max(to, label(mig))
	}
	if from == "" {
		from = "none (empty database)"
	}
	log.Info("database schema", "version", from, "latest", to, "pending", len(pending))
	if len(pending) == 0 {
		log.Info("database schema is up to date")
		return nil
	}

	log.Info("database upgrade needed", "from", from, "to", to, "migrations", names(pending))
	start := time.Now()
	if _, err := m.Migrate(ctx); err != nil {
		log.Error("database upgrade failed", "from", from, "err", err)
		return fmt.Errorf("migrate: %w", err)
	}
	log.Info("database upgraded", "from", from, "to", to, "migrations", len(pending), "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// migrationsOf returns the migrations of an engine: its SQL files and the
// migrations written in Go, the same for both engines.
func migrationsOf(engine string) (*migrate.Migrations, error) {
	fsys, err := fs.Sub(migrations, "migrations/"+engine)
	if err != nil {
		return nil, err
	}
	found := migrate.NewMigrations()
	if err := found.Discover(fsys); err != nil {
		return nil, err
	}
	found.Add(projectRawEvents)
	found.Add(geoSourceIDs)
	return found, nil
}

// ErrSchemaNotCurrent means the database misses migrations this version of
// cauri has, or has some it does not know.
var ErrSchemaNotCurrent = errors.New("database schema is not the one of this version of cauri")

// CheckCurrent returns nil if every migration of this version, and only
// those, is applied to db; it changes nothing, for commands that must only
// read a database (see Migrate to bring it up to date).
func CheckCurrent(ctx context.Context, db *bun.DB) error {
	engine := engineOf(db)
	if old, err := hasTable(ctx, db, engine, "goose_db_version"); err != nil {
		return err
	} else if old {
		return ErrOldSchema
	}
	if ok, err := hasTable(ctx, db, engine, "bun_migrations"); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: no migration applied", ErrSchemaNotCurrent)
	}
	found, err := migrationsOf(engine)
	if err != nil {
		return err
	}
	m := migrate.NewMigrator(db, found)
	if missing, err := m.MissingMigrations(ctx); err != nil {
		return err
	} else if len(missing) > 0 {
		return fmt.Errorf("%w: it has migration %s, unknown to this version", ErrSchemaNotCurrent, label(missing[0]))
	}
	all, err := m.MigrationsWithStatus(ctx)
	if err != nil {
		return err
	}
	if pending := all.Unapplied(); len(pending) > 0 {
		return fmt.Errorf("%w: migration %s not applied", ErrSchemaNotCurrent, label(pending[0]))
	}
	return nil
}

func label(m migrate.Migration) string { return m.Name + "_" + m.Comment }

func names(ms migrate.MigrationSlice) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = label(m)
	}
	return out
}

// engineAttrs describes the engine of db for the log: its version and the
// settings cauri relies on.
func engineAttrs(ctx context.Context, db *bun.DB, engine string) []any {
	attrs := []any{"engine", engine}
	var version string
	if engine == SQLite {
		if db.NewRaw("SELECT sqlite_version()").Scan(ctx, &version) == nil {
			attrs = append(attrs, "version", version)
		}
		var fk, journal string
		if db.NewRaw("PRAGMA foreign_keys").Scan(ctx, &fk) == nil {
			attrs = append(attrs, "foreign_keys", fk == "1")
		}
		if db.NewRaw("PRAGMA journal_mode").Scan(ctx, &journal) == nil {
			attrs = append(attrs, "journal_mode", journal)
		}
		return attrs
	}
	if db.NewRaw("SHOW server_version").Scan(ctx, &version) == nil {
		attrs = append(attrs, "version", version)
	}
	return attrs
}

// hasTable reports whether a table exists, in the current schema for
// PostgreSQL.
func hasTable(ctx context.Context, db bun.IDB, engine, table string) (bool, error) {
	var n int
	q := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	if engine == Postgres {
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?"
	}
	err := db.NewRaw(q, table).Scan(ctx, &n)
	return n > 0, err
}
