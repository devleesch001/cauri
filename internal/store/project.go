package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/migrate"

	"github.com/devleesch001/cauri/internal/cowrie"
)

// projectRawEvents is migration 00009, the same for both engines: written
// in Go because it reads the JSON of the raw lines, which portable SQL
// cannot. It fills what migration 00008 added from the lines already
// stored, as the ingestion service does for each new one (package
// cowrie): events.data, ssh_kex and sessions.ssh_kex_id. A session whose
// key exchange line was purged keeps its HASSH alone. sessions.hassh,
// which ssh_kex now holds, is then dropped.
var projectRawEvents = migrate.Migration{
	Name:    "00009",
	Comment: "project_raw_events",
	Up: func(ctx context.Context, m *migrate.Migrator, _ *migrate.Migration) error {
		return m.DB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return projectUp(ctx, tx)
		})
	},
	Down: func(ctx context.Context, m *migrate.Migrator, _ *migrate.Migration) error {
		return m.DB().RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return projectDown(ctx, tx)
		})
	},
}

// projectBatch is the number of raw lines read at once.
const projectBatch = 1000

func projectUp(ctx context.Context, tx bun.Tx) error {
	kexIDs := map[string]int64{}
	kexID := func(k cowrie.Kex) (int64, error) {
		if id, ok := kexIDs[string(k.Digest)]; ok {
			return id, nil
		}
		row := SSHKex{Digest: k.Digest, HASSH: k.HASSH, Algorithms: k.Algorithms, KeyAlgs: k.KeyAlgs, LangAlgs: k.LangAlgs}
		if _, err := tx.NewInsert().Model(&row).On("CONFLICT (digest) DO NOTHING").Exec(ctx); err != nil {
			return 0, err
		}
		var id int64
		if err := tx.NewSelect().Model((*SSHKex)(nil)).Column("id").Where("digest = ?", k.Digest).Scan(ctx, &id); err != nil {
			return 0, err
		}
		kexIDs[string(k.Digest)] = id
		return id, nil
	}

	// In id order: the first key exchange of a session is the one it
	// keeps, as at ingestion.
	var last int64
	for {
		var lines []struct {
			ID        int64  `bun:"id"`
			Type      string `bun:"type"`
			SessionID *int64 `bun:"session_id"`
			Data      string `bun:"data"`
		}
		if err := tx.NewSelect().TableExpr("events AS e").ColumnExpr("e.id, e.type, e.session_id, ed.data").
			Join("JOIN event_data AS ed ON ed.event_id = e.id").
			Where("e.id > ?", last).OrderExpr("e.id").Limit(projectBatch).Scan(ctx, &lines); err != nil {
			return fmt.Errorf("raw lines: %w", err)
		}
		if len(lines) == 0 {
			break
		}
		for _, l := range lines {
			last = l.ID
			f, ok := cowrie.Parse([]byte(l.Data))
			if !ok {
				continue
			}
			if data := cowrie.Fragment(f, l.Type, l.SessionID != nil); data != nil {
				if _, err := tx.NewUpdate().Model((*Event)(nil)).Set("data = ?", *data).Where("id = ?", l.ID).Exec(ctx); err != nil {
					return fmt.Errorf("event %d: %w", l.ID, err)
				}
			}
			if l.Type != cowrie.ClientKex || l.SessionID == nil {
				continue
			}
			id, err := kexID(cowrie.ParseKex(f))
			if err != nil {
				return fmt.Errorf("key exchange of event %d: %w", l.ID, err)
			}
			if _, err := tx.NewUpdate().Model((*Session)(nil)).Set("ssh_kex_id = ?", id).
				Where("id = ?", *l.SessionID).Where("ssh_kex_id IS NULL").Exec(ctx); err != nil {
				return fmt.Errorf("session %d: %w", *l.SessionID, err)
			}
		}
	}

	// Sessions whose key exchange line is gone.
	var orphans []struct {
		ID    int64  `bun:"id"`
		HASSH string `bun:"hassh"`
	}
	if err := tx.NewSelect().TableExpr("sessions").Column("id", "hassh").
		Where("hassh IS NOT NULL").Where("ssh_kex_id IS NULL").Scan(ctx, &orphans); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("sessions without key exchange line: %w", err)
	}
	for _, o := range orphans {
		id, err := kexID(cowrie.HASSHOnly(o.HASSH))
		if err != nil {
			return fmt.Errorf("HASSH of session %d: %w", o.ID, err)
		}
		if _, err := tx.NewUpdate().Model((*Session)(nil)).Set("ssh_kex_id = ?", id).Where("id = ?", o.ID).Exec(ctx); err != nil {
			return fmt.Errorf("session %d: %w", o.ID, err)
		}
	}
	_, err := tx.ExecContext(ctx, "ALTER TABLE sessions DROP COLUMN hassh")
	return err
}

func projectDown(ctx context.Context, tx bun.Tx) error {
	for _, q := range []string{
		"ALTER TABLE sessions ADD COLUMN hassh TEXT",
		"UPDATE sessions SET hassh = (SELECT k.hassh FROM ssh_kex AS k WHERE k.id = sessions.ssh_kex_id)",
		"UPDATE sessions SET ssh_kex_id = NULL",
		"DELETE FROM ssh_kex",
		"UPDATE events SET data = NULL",
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	return nil
}
