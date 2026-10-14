// Package store opens the database (SQLite or PostgreSQL), applies the
// embedded goose migrations and defines the bun models.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/uptrace/bun/driver/pgdriver"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Supported values of the driver option.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

//go:embed migrations/*.sql
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

// Migrate applies the pending migrations.
func Migrate(ctx context.Context, db *bun.DB) error {
	dialect := goose.DialectSQLite3
	if db.Dialect().Name().String() == "pg" {
		dialect = goose.DialectPostgres
	}
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(dialect, db.DB, fsys)
	if err != nil {
		return err
	}
	if _, err := p.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
