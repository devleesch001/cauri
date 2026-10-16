package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/uptrace/bun"
)

// What the administration shows of the database and does to it. Sizes
// have no portable SQL: this file is the one place that asks each engine
// its own way (dbstat for SQLite, pg_total_relation_size for PostgreSQL).

// DatabaseInfo describes the database: what it is and where its space goes.
type DatabaseInfo struct {
	Engine  string `json:"engine"`
	Version string `json:"version"`
	Schema  string `json:"schema"` // the latest migration applied
	Size    int64  `json:"size"`   // bytes
	// Free is the space the database holds without using it: freed pages
	// a VACUUM gives back to the system (SQLite); 0 when unknown.
	Free   int64       `json:"free"`
	Tables []TableInfo `json:"tables"` // largest first
	Raw    RawInfo     `json:"raw"`
}

// TableInfo is a table, its rows and its size with its indexes.
type TableInfo struct {
	Name  string `json:"name"`
	Rows  int64  `json:"rows"`
	Bytes int64  `json:"bytes"`
}

// RawInfo is what the raw Cowrie lines hold.
type RawInfo struct {
	Lines  int64 `json:"lines"`
	Bytes  int64 `json:"bytes"`  // of the lines themselves
	Oldest *Time `json:"oldest"` // time of the oldest event that has one
}

func engineOf(db bun.IDB) string {
	if db.Dialect().Name().String() == "pg" {
		return Postgres
	}
	return SQLite
}

// GetDatabaseInfo describes the database.
func GetDatabaseInfo(ctx context.Context, db bun.IDB) (*DatabaseInfo, error) {
	d := &DatabaseInfo{Engine: engineOf(db), Tables: []TableInfo{}}
	versionQ := "SELECT sqlite_version()"
	if d.Engine == Postgres {
		versionQ = "SHOW server_version"
	}
	if err := db.NewRaw(versionQ).Scan(ctx, &d.Version); err != nil {
		return nil, err
	}
	if err := db.NewRaw("SELECT name FROM bun_migrations ORDER BY id DESC LIMIT 1").Scan(ctx, &d.Schema); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	var sizes []struct {
		Name  string `bun:"name"`
		Bytes int64  `bun:"bytes"`
	}
	if d.Engine == SQLite {
		var pages, free, pageSize int64
		for q, dst := range map[string]*int64{"PRAGMA page_count": &pages, "PRAGMA freelist_count": &free, "PRAGMA page_size": &pageSize} {
			if err := db.NewRaw(q).Scan(ctx, dst); err != nil {
				return nil, err
			}
		}
		d.Size, d.Free = pages*pageSize, free*pageSize
		// dbstat names tables and indexes: an index counts for its table.
		if err := db.NewRaw(`SELECT m.tbl_name AS name, SUM(s.pgsize) AS bytes
			FROM dbstat AS s JOIN sqlite_master AS m ON m.name = s.name
			WHERE m.tbl_name NOT LIKE 'sqlite_%' AND m.tbl_name NOT LIKE 'bun_%'
			GROUP BY m.tbl_name`).Scan(ctx, &sizes); err != nil {
			return nil, err
		}
	} else {
		if err := db.NewRaw("SELECT pg_database_size(current_database())").Scan(ctx, &d.Size); err != nil {
			return nil, err
		}
		if err := db.NewRaw(`SELECT c.relname AS name, pg_total_relation_size(c.oid) AS bytes
			FROM pg_class AS c JOIN pg_namespace AS n ON n.oid = c.relnamespace
			WHERE n.nspname = current_schema() AND c.relkind = 'r' AND c.relname NOT LIKE 'bun_%'`).Scan(ctx, &sizes); err != nil {
			return nil, err
		}
	}
	for _, s := range sizes {
		t := TableInfo{Name: s.Name, Bytes: s.Bytes}
		n, err := db.NewSelect().TableExpr("?", bun.Ident(s.Name)).Count(ctx)
		if err != nil {
			return nil, err
		}
		t.Rows = n
		d.Tables = append(d.Tables, t)
	}
	slices.SortFunc(d.Tables, func(a, b TableInfo) int { return int(b.Bytes - a.Bytes) })

	raw, err := countRaw(ctx, db, nil)
	if err != nil {
		return nil, err
	}
	d.Raw = raw
	return d, nil
}

// countRaw counts the raw lines of the events before a time, or all.
func countRaw(ctx context.Context, db bun.IDB, before *time.Time) (RawInfo, error) {
	var r struct {
		Lines  int64  `bun:"lines"`
		Bytes  *int64 `bun:"bytes"`
		Oldest *Time  `bun:"oldest"`
	}
	q := db.NewSelect().TableExpr("event_raw_data AS ed").Join("JOIN events AS e ON e.id = ed.event_id").
		ColumnExpr("COUNT(*) AS lines, SUM(LENGTH(ed.data)) AS bytes, MIN(e.ts) AS oldest")
	if before != nil {
		q = q.Where("e.ts < ?", Micro(*before))
	}
	if err := q.Scan(ctx, &r); err != nil {
		return RawInfo{}, err
	}
	out := RawInfo{Lines: r.Lines, Oldest: r.Oldest}
	if r.Bytes != nil {
		out.Bytes = *r.Bytes
	}
	return out, nil
}

// CountRawEvents counts the raw lines a purge of those before a time would
// delete (see PurgeRawEvents), and how large they are.
func CountRawEvents(ctx context.Context, db bun.IDB, before time.Time) (RawInfo, error) {
	return countRaw(ctx, db, &before)
}

// Vacuum compacts the database. With SQLite it rewrites the whole file and
// gives the freed pages back to the system, holding writes meanwhile, then
// empties the write-ahead log; with PostgreSQL it reclaims dead rows for
// reuse and refreshes the planner's statistics, without blocking writes.
func Vacuum(ctx context.Context, db bun.IDB) error {
	if engineOf(db) == Postgres {
		_, err := db.ExecContext(ctx, "VACUUM ANALYZE")
		return err
	}
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)")
	return err
}
