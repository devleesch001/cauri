// Package purge deletes sessions with everything stored about them: their
// rows and the recording files no other session has. "cauri purge" calls
// it; the administration API is to call it too.
//
// Deleted data can come back: an ingestor sending again lines that Cowrie
// still has (a new ingestor started from the beginning, a lost state file)
// stores them again, the deduplication knowing them no more.
package purge

import (
	"context"
	"errors"
	"fmt"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
)

// ErrNoCriteria means a filter that would select every session.
var ErrNoCriteria = errors.New("no criteria: refusing to delete every session")

// batch is the number of sessions deleted per transaction.
const batch = 500

// Purger deletes sessions from the database and the recording volume.
type Purger struct {
	db  *bun.DB
	tty *ttystore.Store
}

// New returns a purger of db and tty.
func New(db *bun.DB, tty *ttystore.Store) *Purger {
	return &Purger{db: db, tty: tty}
}

// Sessions deletes the sessions f selects, its page and sort ignored, and
// returns what went; with apply false, it only counts what would. Running
// it again changes nothing. A recording file that cannot be removed is reported in
// the error, after everything else is deleted: its row is gone already,
// and the file harms nothing.
func (p *Purger) Sessions(ctx context.Context, f store.SessionFilter, apply bool) (store.PurgeCounts, error) {
	return p.SessionsProgress(ctx, f, apply, nil)
}

// SessionsProgress is Sessions telling progress, if not nil, what went so
// far after each batch. It stops between two batches once ctx is
// canceled, returning what went: each batch is whole.
func (p *Purger) SessionsProgress(ctx context.Context, f store.SessionFilter, apply bool, progress func(store.PurgeCounts)) (store.PurgeCounts, error) {
	f.Page, f.Sort, f.Exact = store.Page{}, store.Sort{}, false
	if f == (store.SessionFilter{}) {
		return store.PurgeCounts{}, ErrNoCriteria
	}
	// Never wider than written: a value is exact unless it holds a wildcard.
	f.Exact = true
	if !apply {
		return store.CountPurge(ctx, p.db, f)
	}
	var total store.PurgeCounts
	var errs []error
	for {
		if err := ctx.Err(); err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		c, shas, err := store.DeleteSessionBatch(ctx, p.db, f, batch)
		if err != nil {
			return total, errors.Join(append(errs, err)...)
		}
		if c.Sessions == 0 {
			return total, errors.Join(errs...)
		}
		total.Add(c)
		if progress != nil {
			progress(total)
		}
		for _, sha := range shas {
			if err := p.tty.Remove(sha); err != nil {
				errs = append(errs, fmt.Errorf("recording %s: %w", sha, err))
			}
		}
	}
}
