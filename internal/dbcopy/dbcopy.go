// Package dbcopy copies a cauri database into an empty one, of the same
// engine or the other (SQLite, PostgreSQL), ids included: "cauri db-copy".
//
// The source is only read; it must be at the schema of this version. The
// target is migrated, then filled in one transaction: a failure leaves it
// without rows. Rows go through the bun models, which convert what the
// engines store differently (booleans, bytes).
package dbcopy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
)

// batchSize rows are read and written at once: a few hundred kilobytes of
// sessions or raw lines.
const batchSize = 500

// ErrTargetNotEmpty means the target database already holds rows: a copy
// never merges two databases.
var ErrTargetNotEmpty = errors.New("target database is not empty")

// Count is the rows of a table.
type Count struct {
	Table string
	Rows  int64
}

// table copies one table, by batches in the order of its key.
type table struct {
	name string
	key  string // its integer primary key column
	copy func(ctx context.Context, src bun.IDB, dst bun.Tx, after int64) (last int64, n int, err error)
}

// tables in the order of their foreign keys: a row's parents come first.
// TestTablesCovered checks that it names every table of the schema.
var tables = []table{
	of[store.Sensor]("sensors", "id", func(r *store.Sensor) int64 { return r.ID }),
	of[store.IngestToken]("ingest_tokens", "id", func(r *store.IngestToken) int64 { return r.ID }),
	of[store.GeoSource]("geo_sources", "id", func(r *store.GeoSource) int64 { return r.ID }),
	of[store.Network]("networks", "id", func(r *store.Network) int64 { return r.ID }),
	of[store.SSHKex]("ssh_kex", "id", func(r *store.SSHKex) int64 { return r.ID }),
	of[store.TTYFile]("tty_files", "id", func(r *store.TTYFile) int64 { return r.ID }),
	of[store.Session]("sessions", "id", func(r *store.Session) int64 { return r.ID }),
	of[store.Event]("events", "id", func(r *store.Event) int64 { return r.ID }),
	of[store.EventRawData]("event_raw_data", "event_id", func(r *store.EventRawData) int64 { return r.EventID }),
	of[store.LoginAttempt]("login_attempts", "id", func(r *store.LoginAttempt) int64 { return r.ID }),
	of[store.Command]("commands", "id", func(r *store.Command) int64 { return r.ID }),
	of[store.Transfer]("transfers", "id", func(r *store.Transfer) int64 { return r.ID }),
}

func of[T any](name, key string, keyOf func(*T) int64) table {
	return table{name: name, key: key, copy: func(ctx context.Context, src bun.IDB, dst bun.Tx, after int64) (int64, int, error) {
		var rows []T
		if err := src.NewSelect().Model(&rows).Where("? > ?", bun.Ident(key), after).
			OrderExpr("? ASC", bun.Ident(key)).Limit(batchSize).Scan(ctx); err != nil {
			return after, 0, err
		}
		if len(rows) == 0 {
			return after, 0, nil
		}
		// The ids are given: nothing to return.
		if _, err := dst.NewInsert().Model(&rows).Returning("NULL").Exec(ctx); err != nil {
			return after, 0, err
		}
		return keyOf(&rows[len(rows)-1]), len(rows), nil
	}}
}

// Tables names the copied tables, in the order of the copy.
func Tables() []string {
	out := make([]string, len(tables))
	for i, t := range tables {
		out[i] = t.name
	}
	return out
}

// Counts counts the rows of every copied table of db.
func Counts(ctx context.Context, db bun.IDB) ([]Count, error) {
	out := make([]Count, 0, len(tables))
	for _, t := range tables {
		n, err := db.NewSelect().TableExpr("?", bun.Ident(t.name)).Count(ctx)
		if err != nil {
			return nil, fmt.Errorf("count %s: %w", t.name, err)
		}
		out = append(out, Count{Table: t.name, Rows: n})
	}
	return out, nil
}

// Check returns the source's rows if a copy from src to dst can be made:
// src at the schema of this version, dst without rows (an empty database,
// or one migrated but never used). It changes nothing.
func Check(ctx context.Context, src, dst *bun.DB) ([]Count, error) {
	if err := store.CheckCurrent(ctx, src); err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	counts, err := Counts(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	if err := empty(ctx, dst); err != nil {
		return nil, err
	}
	return counts, nil
}

// empty returns nil if dst holds no row of any table cauri has; tables
// absent count as empty.
func empty(ctx context.Context, dst *bun.DB) error {
	for _, t := range tables {
		var n int
		err := dst.NewRaw("SELECT COUNT(*) FROM (SELECT 1 FROM ? LIMIT 1) AS x", bun.Ident(t.name)).Scan(ctx, &n)
		if err != nil {
			if !missingTable(ctx, dst, t.name) {
				return fmt.Errorf("target: %w", err)
			}
			continue
		}
		if n > 0 {
			return fmt.Errorf("%w: table %s has rows", ErrTargetNotEmpty, t.name)
		}
	}
	return nil
}

// missingTable reports whether the table does not exist in db.
func missingTable(ctx context.Context, db *bun.DB, name string) bool {
	q := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?"
	if db.Dialect().Name().String() == "pg" {
		q = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = ?"
	}
	var n int
	return db.NewRaw(q, name).Scan(ctx, &n) == nil && n == 0
}

// Copy copies src into dst after Check, migrating dst first, and returns
// the rows copied. progress, if not nil, is told each table's rows so far.
// The copy is checked: every table must end with the source's rows.
func Copy(ctx context.Context, src, dst *bun.DB, log *slog.Logger, progress func(Count)) ([]Count, error) {
	want, err := Check(ctx, src, dst)
	if err != nil {
		return nil, err
	}
	if err := store.Migrate(ctx, dst, log); err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	err = dst.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, t := range tables {
			var after int64 = -1 << 63
			var total int64
			for {
				last, n, err := t.copy(ctx, src, tx, after)
				if err != nil {
					return fmt.Errorf("copy %s: %w", t.name, err)
				}
				if n == 0 {
					break
				}
				after, total = last, total+int64(n)
				if progress != nil {
					progress(Count{Table: t.name, Rows: total})
				}
			}
		}
		if dst.Dialect().Name().String() == "pg" {
			return resetIdentities(ctx, tx)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	got, err := Counts(ctx, dst)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	for i := range want {
		if got[i] != want[i] {
			return got, fmt.Errorf("table %s: %d rows copied, the source has %d", want[i].Table, got[i].Rows, want[i].Rows)
		}
	}
	return got, nil
}

// resetIdentities moves each PostgreSQL identity past the ids copied: the
// rows were inserted with their ids, which identities do not see, and the
// API's next insert would collide with them.
func resetIdentities(ctx context.Context, tx bun.Tx) error {
	for _, t := range tables {
		if t.key != "id" {
			continue
		}
		if _, err := tx.NewRaw("SELECT setval(pg_get_serial_sequence(?, 'id'), COALESCE(MAX(id), 1), MAX(id) IS NOT NULL) FROM ?",
			t.name, bun.Ident(t.name)).Exec(ctx); err != nil {
			return fmt.Errorf("identity of %s: %w", t.name, err)
		}
	}
	return nil
}
