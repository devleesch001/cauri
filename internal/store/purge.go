package store

import (
	"context"

	"github.com/uptrace/bun"
)

// PurgeCounts tells what deleting sessions removes, or would remove. The
// raw lines (event_data) go with their events.
type PurgeCounts struct {
	Sessions      int `json:"sessions"`
	Events        int `json:"events"`
	LoginAttempts int `json:"login_attempts"`
	Commands      int `json:"commands"`
	Transfers     int `json:"transfers"`
	// Recordings no session is left to refer to: their rows and files go.
	Recordings int `json:"recordings"`
}

// Add adds o to c.
func (c *PurgeCounts) Add(o PurgeCounts) {
	c.Sessions += o.Sessions
	c.Events += o.Events
	c.LoginAttempts += o.LoginAttempts
	c.Commands += o.Commands
	c.Transfers += o.Transfers
	c.Recordings += o.Recordings
}

// ids selects the ids of the sessions f matches, its page ignored.
func (f SessionFilter) ids(db bun.IDB) *bun.SelectQuery {
	q := db.NewSelect().TableExpr("sessions AS s").ColumnExpr("s.id").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id")
	return f.where(db, q)
}

// orphaned selects the recordings of the sessions sel selects that no
// other session refers to.
func orphaned(db bun.IDB, sel *bun.SelectQuery) *bun.SelectQuery {
	return db.NewSelect().TableExpr("tty_files AS tf").
		Where("tf.id IN (?)", db.NewSelect().TableExpr("sessions AS rs").ColumnExpr("rs.tty_file_id").Where("rs.id IN (?)", sel)).
		Where("NOT EXISTS (?)", db.NewSelect().TableExpr("sessions AS os").ColumnExpr("1").
			Where("os.tty_file_id = tf.id").Where("os.id NOT IN (?)", sel))
}

func countPurge(ctx context.Context, db bun.IDB, sel *bun.SelectQuery) (PurgeCounts, error) {
	var c PurgeCounts
	for _, n := range []struct {
		dst *int
		q   *bun.SelectQuery
	}{
		{&c.Sessions, db.NewSelect().TableExpr("sessions AS ps").Where("ps.id IN (?)", sel)},
		{&c.Events, db.NewSelect().TableExpr("events AS pe").Where("pe.session_id IN (?)", sel)},
		{&c.LoginAttempts, db.NewSelect().TableExpr("login_attempts AS pl").Where("pl.session_id IN (?)", sel)},
		{&c.Commands, db.NewSelect().TableExpr("commands AS pc").Where("pc.session_id IN (?)", sel)},
		{&c.Transfers, db.NewSelect().TableExpr("transfers AS px").Where("px.session_id IN (?)", sel)},
		{&c.Recordings, orphaned(db, sel)},
	} {
		v, err := n.q.Count(ctx)
		if err != nil {
			return c, err
		}
		*n.dst = int(v)
	}
	return c, nil
}

// CountPurge counts what deleting the sessions f selects would remove.
func CountPurge(ctx context.Context, db bun.IDB, f SessionFilter) (PurgeCounts, error) {
	return countPurge(ctx, db, f.ids(db))
}

// DeleteSessionBatch deletes, in one transaction, up to n of the sessions
// f selects, everything that hangs on them (by the foreign keys' cascades)
// and the recordings no other session refers to. It returns what it
// removed and the digests of those recordings, whose files the caller
// removes once this returns. No session removed means none is left.
func DeleteSessionBatch(ctx context.Context, db bun.IDB, f SessionFilter, n int) (PurgeCounts, []string, error) {
	var c PurgeCounts
	var shas []string
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var ids []int64
		if err := f.ids(tx).OrderExpr("s.id").Limit(int64(n)).Scan(ctx, &ids); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		sel := tx.NewSelect().TableExpr("sessions AS bs").ColumnExpr("bs.id").Where("bs.id IN (?)", bun.List(ids))
		var err error
		if c, err = countPurge(ctx, tx, sel); err != nil {
			return err
		}
		if err := orphaned(tx, sel).ColumnExpr("tf.sha256").Scan(ctx, &shas); err != nil {
			return err
		}
		if _, err := tx.NewDelete().TableExpr("sessions").Where("id IN (?)", bun.List(ids)).Exec(ctx); err != nil {
			return err
		}
		if len(shas) > 0 {
			_, err = tx.NewDelete().TableExpr("tty_files").Where("sha256 IN (?)", bun.List(shas)).Exec(ctx)
		}
		return err
	})
	if err != nil {
		return PurgeCounts{}, nil, err
	}
	return c, shas, nil
}
