package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

func TestMigrateIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		if err := store.Migrate(context.Background(), db); err != nil {
			t.Fatal(err)
		}
	})
}

// A database of cauri v0.0.x is refused, not migrated beside its tables.
func TestMigrateRefusesOldSchema(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, store.SQLite, filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, "CREATE TABLE goose_db_version (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); !errors.Is(err, store.ErrOldSchema) {
		t.Fatalf("err = %v, want ErrOldSchema", err)
	}
}

// TestTimeRoundTrip checks that times survive the database to the
// microsecond and sort chronologically.
func TestTimeRoundTrip(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		base := time.Date(2026, 10, 14, 9, 0, 5, 0, time.UTC)
		times := []time.Time{
			base.Add(500 * time.Millisecond),
			base,
			base.Add(123456 * time.Microsecond),
		}
		for i, ts := range times {
			st := store.NewTime(ts)
			s := &store.Sensor{Name: string(rune('a' + i)), FirstSeen: &st, LastSeen: &st}
			if _, err := db.NewInsert().Model(s).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		var got []store.Sensor
		if err := db.NewSelect().Model(&got).Order("first_seen").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		want := []string{"b", "c", "a"}
		for i, s := range got {
			if s.Name != want[i] {
				t.Fatalf("order = %v, want %v", got, want)
			}
		}
		if !got[1].FirstSeen.Equal(times[2]) {
			t.Errorf("time = %v, want %v", got[1].FirstSeen, times[2])
		}
		n, err := db.NewSelect().Model((*store.Sensor)(nil)).Where("first_seen > ?", store.NewTime(base)).Count(ctx)
		if err != nil || n != 2 {
			t.Errorf("count after base = %d, %v", n, err)
		}
		var empty store.Sensor
		if err := db.NewSelect().Model(&empty).Where("name = 'a'").Scan(ctx); err != nil || empty.FirstSeen == nil {
			t.Errorf("scan = %+v, %v", empty, err)
		}
		if _, err := db.NewInsert().Model(&store.Sensor{Name: "never"}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var never store.Sensor
		if err := db.NewSelect().Model(&never).Where("name = 'never'").Scan(ctx); err != nil || never.FirstSeen != nil {
			t.Errorf("null time = %+v, %v", never.FirstSeen, err)
		}
	})
}

func TestForeignKeys(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		_, err := db.NewInsert().Model(&store.Session{
			SensorID: 42, CowrieSession: "s", StartedAt: store.NewTime(time.Now()),
		}).Exec(context.Background())
		if err == nil {
			t.Fatal("want foreign key violation")
		}
	})
}

func TestOpenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := store.Open(ctx, "mysql", "x"); err == nil {
		t.Error("want error for unknown driver")
	}
	if _, err := store.Open(ctx, store.SQLite, ""); err == nil {
		t.Error("want error for empty dsn")
	}
}

// The two migration sets must describe the same tables, columns, nullness
// and indexes; only the types differ.
func TestSchemasMatch(t *testing.T) {
	if os.Getenv(storetest.EnvPostgres) == "" {
		t.Skip(storetest.EnvPostgres + " not set")
	}
	ctx := context.Background()
	lite := schema(t, ctx, storetest.SQLite(t), store.SQLite)
	pg := schema(t, ctx, storetest.Postgres(t), store.Postgres)
	if len(lite) < 80 {
		t.Fatalf("schema read back has %d lines: the queries miss something", len(lite))
	}
	if !slices.Equal(lite, pg) {
		for _, l := range lite {
			if !slices.Contains(pg, l) {
				t.Errorf("only in SQLite: %s", l)
			}
		}
		for _, l := range pg {
			if !slices.Contains(lite, l) {
				t.Errorf("only in PostgreSQL: %s", l)
			}
		}
	}
}

// schema lists "table.column null|not null" and "index table(columns)
// [unique]" lines, sorted, without bun's own tables.
func schema(t *testing.T, ctx context.Context, db *bun.DB, engine string) []string {
	t.Helper()
	var lines []string
	add := func(q string) {
		var rows []string
		if err := db.NewRaw(q).Scan(ctx, &rows); err != nil {
			t.Fatalf("%s: %v", engine, err)
		}
		for _, r := range rows {
			if !strings.HasPrefix(r, "bun_") && !strings.HasPrefix(r, "index bun_") {
				lines = append(lines, r)
			}
		}
	}
	if engine == store.SQLite {
		add(`SELECT m.name || '.' || c.name || CASE WHEN c."notnull" = 1 OR c.pk = 1 THEN ' not null' ELSE ' null' END
			FROM sqlite_master AS m, pragma_table_info(m.name) AS c
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%'`)
		// Named indexes, and the unique constraints SQLite indexes itself.
		add(`SELECT 'index ' || m.name || '(' || (SELECT group_concat(name, ',') FROM (SELECT ii.name FROM pragma_index_info(il.name) AS ii ORDER BY ii.seqno)) || ')' ||
				CASE WHEN il."unique" = 1 THEN ' unique' ELSE '' END
			FROM sqlite_master AS m, pragma_index_list(m.name) AS il
			WHERE m.type = 'table' AND m.name NOT LIKE 'sqlite_%' AND il.origin != 'pk'`)
	} else {
		add(`SELECT table_name || '.' || column_name || CASE WHEN is_nullable = 'NO' THEN ' not null' ELSE ' null' END
			FROM information_schema.columns WHERE table_schema = current_schema()`)
		add(`SELECT 'index ' || t.relname || '(' || string_agg(a.attname, ',' ORDER BY k.n) || ')' ||
				CASE WHEN ix.indisunique THEN ' unique' ELSE '' END
			FROM pg_index AS ix
			JOIN pg_class AS t ON t.oid = ix.indrelid
			JOIN pg_namespace AS ns ON ns.oid = t.relnamespace
			CROSS JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, n)
			JOIN pg_attribute AS a ON a.attrelid = t.oid AND a.attnum = k.attnum
			WHERE ns.nspname = current_schema() AND NOT ix.indisprimary
			GROUP BY t.relname, ix.indexrelid, ix.indisunique`)
	}
	slices.Sort(lines)
	return lines
}
