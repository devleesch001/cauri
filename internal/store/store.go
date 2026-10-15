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
	"strings"

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

// Migrate applies the pending migrations of the engine of db.
func Migrate(ctx context.Context, db *bun.DB) error {
	engine := SQLite
	if db.Dialect().Name().String() == "pg" {
		engine = Postgres
	}
	if old, err := hasTable(ctx, db, engine, "goose_db_version"); err != nil {
		return err
	} else if old {
		return ErrOldSchema
	}
	fsys, err := fs.Sub(migrations, "migrations/"+engine)
	if err != nil {
		return err
	}
	ms := migrate.NewMigrations()
	if err := ms.Discover(fsys); err != nil {
		return err
	}
	// Marked applied only once run: by default bun marks a migration first,
	// so a failed one would count as done. No Lock: bun's lock is a row
	// left behind if the API dies while migrating, blocking every start;
	// one API runs per database.
	m := migrate.NewMigrator(db, ms, migrate.WithMarkAppliedOnSuccess(true))
	if err := m.Init(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := m.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
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
