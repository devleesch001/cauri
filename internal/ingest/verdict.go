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
func (s *Service) JudgeSession(ctx context.Context, id int64) (verdict.Result, error) {
	res, _, err := s.judge(ctx, s.db, id)
	return res, err
}

// judge also tells, from the same reading of the recording, whether the
// session typed in an interactive shell: nil without a recording to read.
func (s *Service) judge(ctx context.Context, db bun.IDB, id int64) (verdict.Result, *bool, error) {
	facts, err := store.GetSessionFacts(ctx, db, id)
	if err != nil {
		return verdict.Result{}, nil, err
	}
	// A recording missing from the volume or unreadable leaves the events
	// alone to judge: it must not block the ingestion of events.
	var typing *verdict.Typing
	var shell *bool
	if facts.RecordingSHA256 != nil {
		if f, err := s.tty.Open(*facts.RecordingSHA256); err == nil {
			ty, err := verdict.ReadTyping(f)
			_ = f.Close()
			if err == nil {
				typing = &ty
				shell = new(ty.Shell())
			}
		}
	}
	return verdict.Judge(verdict.Facts{
		Terminal: facts.Terminal, Commands: facts.Commands, ClientVersion: facts.ClientVersion,
		Channels: facts.Channels,
	}, typing), shell, nil
}

// storeVerdict judges sessions and stores their verdict and whether they
// typed in a shell, to filter the session list. It runs in the transaction
// that changed them, after the change: the row lock taken by the change
// orders concurrent judgements.
func (s *Service) storeVerdict(ctx context.Context, db bun.IDB, ids ...int64) error {
	for _, id := range ids {
		res, shell, err := s.judge(ctx, db, id)
		if err != nil {
			return err
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("verdict = ?", string(res.Verdict)).
			// A recording gone since (retention) keeps what it told.
			Set("shell = COALESCE(?, shell)", shell).
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
	if err := s.keepVerdictsWithoutRecording(ctx); err != nil {
		return 0, err
	}
	total := 0
	for {
		var ids []int64
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

// keepVerdictsWithoutRecording moves to the current version the verdicts of
// sessions that had one recording, which is gone (an operator removed the
// file to limit retention): judging them again could only see the events,
// and would lose what the typing told. A session of one channel or none is
// judged by the same criteria whatever their version (only the number of
// channels was added), so its stored verdict stands. Sessions of several
// channels have no recording and are judged again.
func (s *Service) keepVerdictsWithoutRecording(ctx context.Context) error {
	_, err := s.db.NewUpdate().Model((*store.Session)(nil)).
		Set("verdict_version = ?", verdict.Version).
		Where("verdict IS NOT NULL").Where("verdict_version IS NOT NULL").Where("verdict_version <> ?", verdict.Version).
		Where("tty_log IS NOT NULL").Where("tty_file_id IS NULL").
		Where("(SELECT COUNT(*) FROM events AS e WHERE e.session_id = s.id AND e.type = ?) <= 1", "cowrie.log.closed").
		Exec(ctx)
	return err
}
