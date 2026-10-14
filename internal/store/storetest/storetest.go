// Package storetest provides migrated, isolated databases to tests.
//
// SQLite is always tested. PostgreSQL is tested too when CAURI_TEST_PG_DSN
// holds a connection URL; each test then gets its own schema.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"cauri/internal/store"
)

// EnvPostgres names the variable holding the test PostgreSQL URL.
const EnvPostgres = "CAURI_TEST_PG_DSN"

// Run runs f once per available database, each in a subtest.
func Run(t *testing.T, f func(t *testing.T, db *bun.DB)) {
	t.Helper()
	t.Run(store.SQLite, func(t *testing.T) { f(t, SQLite(t)) })
	if os.Getenv(EnvPostgres) != "" {
		t.Run(store.Postgres, func(t *testing.T) { f(t, Postgres(t)) })
	}
}

// SQLite returns a migrated database in a temporary file.
func SQLite(t *testing.T) *bun.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, store.SQLite, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

// Postgres returns a migrated database in a fresh schema, dropped at the end
// of the test. It skips the test if EnvPostgres is unset.
func Postgres(t *testing.T) *bun.DB {
	t.Helper()
	dsn := os.Getenv(EnvPostgres)
	if dsn == "" {
		t.Skip(EnvPostgres + " not set")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "test_" + hex.EncodeToString(b)

	admin, err := store.Open(ctx, store.Postgres, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})

	sqldb := sql.OpenDB(pgdriver.NewConnector(
		pgdriver.WithDSN(dsn),
		pgdriver.WithConnParams(map[string]any{"search_path": schema}),
	))
	db := bun.NewDB(sqldb, pgdialect.New())
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}
