package store_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// A GeoIP database is stored once per type and build; only those that
// located a session are due an attribution; one removed leaves its
// sessions to be located again.
func TestGeoSources(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		oct := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		a, err := store.GeoSourceID(ctx, db, "DBIP-City-Lite", oct)
		if err != nil {
			t.Fatal(err)
		}
		if again, err := store.GeoSourceID(ctx, db, "DBIP-City-Lite", oct); err != nil || again != a {
			t.Errorf("same database: %d, %v; want %d", again, err, a)
		}
		b, err := store.GeoSourceID(ctx, db, "DBIP-City-Lite", oct.AddDate(0, 1, 0))
		if err != nil || b == a {
			t.Errorf("next build: %d, %v", b, err)
		}
		if _, err := store.GeoSourceID(ctx, db, "GeoLite2-City", oct); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("geo_source_id = ?", a).
			Where("id = ?", lookup(t, db, "hp-01", "1")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if types, err := store.ListGeoSources(ctx, db); err != nil || !slices.Equal(types, []string{"DBIP-City-Lite"}) {
			t.Errorf("sources in use = %v, %v", types, err)
		}
		s, err := store.GetSession(ctx, db, lookup(t, db, "hp-01", "1"))
		if err != nil || *s.GeoSourceType != "DBIP-City-Lite" || !s.GeoSourceBuilt.Equal(oct) {
			t.Fatalf("session source = %v %v, %v", s.GeoSourceType, s.GeoSourceBuilt, err)
		}
		if _, err := db.NewDelete().Model((*store.GeoSource)(nil)).Where("id = ?", a).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if s, _ := store.GetSession(ctx, db, lookup(t, db, "hp-01", "1")); s.GeoSourceID != nil {
			t.Errorf("source still referenced: %v", *s.GeoSourceID)
		}
	})
}

// Migration 00011 turns the text of each session into a reference, from
// the schema of 00010 (its down migration).
func TestGeoSourceIDs(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		inTx := func(fn func(context.Context, bun.Tx) error) {
			t.Helper()
			if err := db.RunInTx(ctx, nil, fn); err != nil {
				t.Fatal(err)
			}
		}
		inTx(store.GeoSourcesDown)
		for sess, src := range map[string]string{"1": "DBIP-City-Lite 2026-10-01", "2": "DBIP-City-Lite 2026-10-01", "3": "unreadable"} {
			if _, err := db.ExecContext(ctx, "UPDATE sessions SET geo_source = ? WHERE cowrie_session = ?", src, sess); err != nil {
				t.Fatal(err)
			}
		}
		inTx(store.GeoSourcesUp)

		var sources []store.GeoSource
		if err := db.NewSelect().Model(&sources).OrderExpr("type").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(sources) != 2 || sources[0].Type != "DBIP-City-Lite" || !sources[0].Built.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) ||
			sources[1].Type != "unreadable" || sources[1].Built.Unix() != 0 {
			t.Fatalf("geo_sources = %+v", sources)
		}
		var refs []int64
		if err := db.NewSelect().Model((*store.Session)(nil)).Column("geo_source_id").Where("geo_source_id IS NOT NULL").
			OrderExpr("cowrie_session").Scan(ctx, &refs); err != nil || len(refs) != 3 || refs[0] != refs[1] || refs[2] == refs[0] {
			t.Errorf("references = %v, %v", refs, err)
		}
		// Qualified: SQLite reads an unknown quoted name as a string.
		var gone []string
		if err := db.NewSelect().TableExpr("sessions AS s").ColumnExpr("s.geo_source").Scan(ctx, &gone); err == nil {
			t.Error("sessions.geo_source is still there")
		}
	})
}

// Migration 00012 moves the networks of sessions to their own table, once
// per ASN, and back.
func TestNetworksMigration(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seed(t, db)
		ctx := context.Background()
		engine := store.SQLite
		if db.Dialect().Name().String() == "pg" {
			engine = store.Postgres
		}
		run := func(file string) {
			t.Helper()
			qs, err := store.MigrationSQL(engine, file)
			if err != nil {
				t.Fatal(err)
			}
			err = db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				for _, q := range qs {
					if _, err := tx.ExecContext(ctx, q); err != nil {
						return fmt.Errorf("%s: %w", q, err)
					}
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		run("00012_networks.tx.down.sql")
		for sess, asn := range map[string]int64{"1": 4134, "2": 4134, "3": 64500} {
			if _, err := db.ExecContext(ctx, "UPDATE sessions SET asn = ?, as_org = ? WHERE cowrie_session = ?", asn, fmt.Sprint("AS", asn), sess); err != nil {
				t.Fatal(err)
			}
		}
		run("00012_networks.tx.up.sql")

		var nets []store.Network
		if err := db.NewSelect().Model(&nets).OrderExpr("asn").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(nets) != 2 || nets[0].ASN != 4134 || *nets[0].Org != "AS4134" || nets[1].ASN != 64500 {
			t.Fatalf("networks = %+v", nets)
		}
		if _, got := sessions(t, db, store.SessionFilter{ASN: 4134}); got != "21" {
			t.Errorf("sessions of AS4134 = %q", got)
		}
		s, err := store.GetSession(ctx, db, lookup(t, db, "hp-01", "1"))
		if err != nil || *s.ASN != 4134 || *s.ASOrg != "AS4134" {
			t.Errorf("session network = %v %v, %v", s.ASN, s.ASOrg, err)
		}

		run("00012_networks.tx.down.sql")
		var asn []int64
		if err := db.NewSelect().TableExpr("sessions AS s").ColumnExpr("s.asn").Where("s.asn IS NOT NULL").
			OrderExpr("s.asn").Scan(ctx, &asn); err != nil || !slices.Equal(asn, []int64{4134, 4134, 64500}) {
			t.Errorf("asn back in sessions = %v, %v", asn, err)
		}
		run("00012_networks.tx.up.sql")
	})
}
