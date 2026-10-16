package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"
)

// geoSourceIDs is migration 00011, the same for both engines: written in
// Go because it parses the text sessions.geo_source ("DBIP-City-Lite
// 2026-10-01", geoip.Info.Source), whose date portable SQL cannot read.
// It fills geo_sources and sessions.geo_source_id, which migration 00010
// added, then drops sessions.geo_source.
var geoSourceIDs = migrate.Migration{
	Name:    "00011",
	Comment: "geo_source_ids",
	Up: func(ctx context.Context, m *migrate.Migrator, _ *migrate.Migration) error {
		return m.DB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return geoSourcesUp(ctx, tx)
		})
	},
	Down: func(ctx context.Context, m *migrate.Migrator, _ *migrate.Migration) error {
		return m.DB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return geoSourcesDown(ctx, tx)
		})
	},
}

// parseGeoSource splits a geoip.Info.Source. cauri wrote them all; one it
// cannot read keeps its whole text as type, built at the epoch.
func parseGeoSource(s string) (typ string, built time.Time) {
	if t, date, ok := strings.Cut(s, " "); ok {
		if b, err := time.Parse(time.DateOnly, date); err == nil {
			return t, b
		}
	}
	return s, time.Unix(0, 0).UTC()
}

func geoSourcesUp(ctx context.Context, tx bun.Tx) error {
	var sources []string
	if err := tx.NewSelect().TableExpr("sessions").ColumnExpr("DISTINCT geo_source").
		Where("geo_source IS NOT NULL").Scan(ctx, &sources); err != nil {
		return fmt.Errorf("sources: %w", err)
	}
	for _, src := range sources {
		typ, built := parseGeoSource(src)
		id, err := GeoSourceID(ctx, tx, typ, built)
		if err != nil {
			return fmt.Errorf("source %q: %w", src, err)
		}
		if _, err := tx.NewUpdate().Model((*Session)(nil)).Set("geo_source_id = ?", id).
			Where("geo_source = ?", src).Exec(ctx); err != nil {
			return fmt.Errorf("sessions of %q: %w", src, err)
		}
	}
	_, err := tx.ExecContext(ctx, "ALTER TABLE sessions DROP COLUMN geo_source")
	return err
}

func geoSourcesDown(ctx context.Context, tx bun.Tx) error {
	if _, err := tx.ExecContext(ctx, "ALTER TABLE sessions ADD COLUMN geo_source TEXT"); err != nil {
		return err
	}
	var sources []GeoSource
	if err := tx.NewSelect().Model(&sources).Scan(ctx); err != nil {
		return err
	}
	for _, g := range sources {
		src := g.Type + " " + g.Built.UTC().Format(time.DateOnly)
		if _, err := tx.NewUpdate().Model((*Session)(nil)).Set("geo_source = ?", src).
			Where("geo_source_id = ?", g.ID).Exec(ctx); err != nil {
			return err
		}
	}
	for _, q := range []string{"UPDATE sessions SET geo_source_id = NULL", "DELETE FROM geo_sources"} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
