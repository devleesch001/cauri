package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// purgeBatch bounds the raw lines deleted per statement, so that a purge
// never holds the database for long.
const purgeBatch = 5000

// PurgeRawEvents deletes the raw Cowrie lines (event_data) of the events
// before the given time and returns how many it deleted. Sessions, events,
// their data, projections and deduplication stay: what the tables do not
// hold is in events.data, so only the raw view of old events and their
// reprocessing are lost.
func PurgeRawEvents(ctx context.Context, db bun.IDB, before time.Time) (int, error) {
	total := 0
	for {
		old := db.NewSelect().TableExpr("events AS pe").ColumnExpr("pe.id").
			Join("JOIN event_data AS ped ON ped.event_id = pe.id").
			Where("pe.ts < ?", Micro(before)).
			OrderExpr("pe.id").Limit(purgeBatch)
		res, err := db.NewDelete().Model((*EventData)(nil)).Where("event_id IN (?)", old).Exec(ctx)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += int(n)
		if n < purgeBatch {
			return total, nil
		}
	}
}
