package ingest

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/verdict"
)

// JudgeSession tells whether a session looks typed by a human or run by a
// bot, from its recording and its events (see package verdict). It returns
// store.ErrNotFound for an unknown session.
func (s *Service) JudgeSession(ctx context.Context, id string) (verdict.Result, error) {
	return s.judge(ctx, s.db, id)
}

func (s *Service) judge(ctx context.Context, db bun.IDB, id string) (verdict.Result, error) {
	facts, err := store.GetSessionFacts(ctx, db, id)
	if err != nil {
		return verdict.Result{}, err
	}
	// A recording missing from the volume or unreadable leaves the events
	// alone to judge: it must not block the ingestion of events.
	var typing *verdict.Typing
	if facts.RecordingSHA256 != nil {
		if f, err := s.tty.Open(*facts.RecordingSHA256); err == nil {
			ty, err := verdict.ReadTyping(f)
			_ = f.Close()
			if err == nil {
				typing = &ty
			}
		}
	}
	return verdict.Judge(verdict.Facts{
		Terminal: facts.Terminal, Commands: facts.Commands, ClientVersion: facts.ClientVersion,
	}, typing), nil
}

// storeVerdict judges sessions and stores their verdict, to filter the
// session list. It runs in the transaction that changed them, after the
// change: the row lock taken by the change orders concurrent judgements.
func (s *Service) storeVerdict(ctx context.Context, db bun.IDB, ids ...string) error {
	for _, id := range ids {
		res, err := s.judge(ctx, db, id)
		if err != nil {
			return err
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("verdict = ?", string(res.Verdict)).
			Set("verdict_score = ?", res.Score).
			Set("verdict_version = ?", verdict.Version).
			Where("id = ?", id).Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// judgeBatch is the number of sessions judged per transaction.
const judgeBatch = 200

// JudgeSessions stores the verdict of the sessions judged by other
// criteria than the current ones, or never judged (stored before verdicts
// were), and returns how many it judged.
func (s *Service) JudgeSessions(ctx context.Context) (int, error) {
	total := 0
	for {
		var ids []string
		err := s.db.NewSelect().Model((*store.Session)(nil)).Column("id").
			Where("verdict_version IS NULL OR verdict_version <> ?", verdict.Version).
			OrderExpr("s.id").Limit(judgeBatch).Scan(ctx, &ids)
		if err != nil || len(ids) == 0 {
			return total, err
		}
		err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			return s.storeVerdict(ctx, tx, ids...)
		})
		if err != nil {
			return total, err
		}
		total += len(ids)
	}
}
