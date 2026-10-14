package ingest

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"cauri/internal/store"
)

// batchWriter accumulates the rows produced by the new events of a batch,
// then writes them table by table.
//
// Sessions are merged with rules that do not depend on arrival order, in
// memory within a batch and in SQL across batches (see upsertSessions):
// connection details keep their first value, started_at the earliest
// timestamp, end and recording details their last value, login_success
// becomes true once any login succeeded. The digest and size of the
// recording are only set by StoreTTY, from the uploaded file.
type batchWriter struct {
	now      time.Time
	sensor   store.Sensor
	sessions map[string]*store.Session
	order    []string // session ids, in order of appearance
	raw      []store.RawEvent
	logins   []store.LoginAttempt
	commands []store.Command
	xfers    []store.Transfer
}

func newBatchWriter(sensorID string, now time.Time) *batchWriter {
	return &batchWriter{
		now:      now,
		sensor:   store.Sensor{ID: sensorID},
		sessions: map[string]*store.Session{},
	}
}

func (w *batchWriter) add(id, raw string, ev *cowrieEvent) {
	if w.sensor.FirstSeen.IsZero() || ev.ts.Before(w.sensor.FirstSeen) {
		w.sensor.FirstSeen = ev.ts
	}
	if ev.ts.After(w.sensor.LastSeen) {
		w.sensor.LastSeen = ev.ts
	}

	var sessionID *string
	if ev.Session != "" {
		s := w.session(ev)
		sessionID = &s.ID
		w.project(id, s, ev)
	}
	w.raw = append(w.raw, store.RawEvent{
		ID: id, SensorID: w.sensor.ID, SessionID: sessionID, EventID: ev.EventID,
		TS: ev.ts, SrcIP: ev.SrcIP, Data: raw, ReceivedAt: w.now,
	})
}

// session returns the pending session row of ev, created on first use.
func (w *batchWriter) session(ev *cowrieEvent) *store.Session {
	id := store.SessionID(w.sensor.ID, ev.Session)
	s, ok := w.sessions[id]
	if !ok {
		s = &store.Session{ID: id, SensorID: w.sensor.ID, Session: ev.Session, StartedAt: ev.ts}
		w.sessions[id] = s
		w.order = append(w.order, id)
	}
	if ev.ts.Before(s.StartedAt) {
		s.StartedAt = ev.ts
	}
	firstStr(&s.SrcIP, ev.SrcIP)
	return s
}

// project turns a known event into session fields and table rows.
func (w *batchWriter) project(id string, s *store.Session, ev *cowrieEvent) {
	switch ev.EventID {
	case evConnect:
		firstInt(&s.SrcPort, ev.SrcPort)
		firstStr(&s.DstIP, ev.DstIP)
		firstInt(&s.DstPort, ev.DstPort)
		firstStr(&s.Protocol, ev.Protocol)
	case evClientVersion:
		firstStr(&s.ClientVersion, ev.Version)
	case evClientKex:
		firstStr(&s.HASSH, ev.HASSH)
	case evLoginSuccess, evLoginFailed:
		success := ev.EventID == evLoginSuccess
		if success {
			s.LoginSuccess = true
		}
		w.logins = append(w.logins, store.LoginAttempt{
			ID: id, SessionID: s.ID, TS: ev.ts,
			Username: deref(ev.Username), Password: deref(ev.Password), Success: success,
		})
	case evCommandInput:
		w.commands = append(w.commands, store.Command{ID: id, SessionID: s.ID, TS: ev.ts, Input: deref(ev.Input)})
	case evFileDownload:
		w.xfers = append(w.xfers, store.Transfer{
			ID: id, SessionID: s.ID, TS: ev.ts, Direction: store.Download,
			URL: ev.URL, Filename: ev.DestFile, SHA256: ev.Shasum,
		})
	case evFileUpload:
		w.xfers = append(w.xfers, store.Transfer{
			ID: id, SessionID: s.ID, TS: ev.ts, Direction: store.Upload,
			Filename: ev.Filename, SHA256: ev.Shasum,
		})
	case evLogClosed:
		if name := ev.ttyName(); name != nil {
			s.TTYLog = name
		}
		if d := ev.seconds(); d != nil {
			s.TTYDuration = d
		}
	case evSessionClosed:
		ts := ev.ts
		s.EndedAt = &ts
		if d := ev.seconds(); d != nil {
			s.Duration = d
		}
	}
}

func (w *batchWriter) write(ctx context.Context, tx bun.Tx) error {
	if _, err := tx.NewInsert().Model(&w.sensor).
		On("CONFLICT (id) DO UPDATE").
		Set("first_seen = CASE WHEN EXCLUDED.first_seen < ?TableAlias.first_seen THEN EXCLUDED.first_seen ELSE ?TableAlias.first_seen END").
		Set("last_seen = CASE WHEN EXCLUDED.last_seen > ?TableAlias.last_seen THEN EXCLUDED.last_seen ELSE ?TableAlias.last_seen END").
		Exec(ctx); err != nil {
		return err
	}
	if err := w.upsertSessions(ctx, tx); err != nil {
		return err
	}
	// Concurrent duplicates are possible on PostgreSQL: ignore conflicts.
	if err := insertIgnore(ctx, tx, &w.raw); err != nil {
		return err
	}
	if err := insertIgnore(ctx, tx, &w.logins); err != nil {
		return err
	}
	if err := insertIgnore(ctx, tx, &w.commands); err != nil {
		return err
	}
	return insertIgnore(ctx, tx, &w.xfers)
}

func (w *batchWriter) upsertSessions(ctx context.Context, tx bun.Tx) error {
	if len(w.order) == 0 {
		return nil
	}
	rows := make([]store.Session, len(w.order))
	for i, id := range w.order {
		rows[i] = *w.sessions[id]
	}
	q := tx.NewInsert().Model(&rows).On("CONFLICT (id) DO UPDATE").
		Set("started_at = CASE WHEN EXCLUDED.started_at < ?TableAlias.started_at THEN EXCLUDED.started_at ELSE ?TableAlias.started_at END").
		Set("login_success = (?TableAlias.login_success OR EXCLUDED.login_success)")
	for _, col := range []string{"src_ip", "src_port", "dst_ip", "dst_port", "protocol", "client_version", "hassh"} {
		q = q.Set(col + " = COALESCE(?TableAlias." + col + ", EXCLUDED." + col + ")")
	}
	for _, col := range []string{"ended_at", "duration", "tty_log", "tty_duration"} {
		q = q.Set(col + " = COALESCE(EXCLUDED." + col + ", ?TableAlias." + col + ")")
	}
	_, err := q.Exec(ctx)
	return err
}

func insertIgnore[T any](ctx context.Context, tx bun.Tx, rows *[]T) error {
	if len(*rows) == 0 {
		return nil
	}
	_, err := tx.NewInsert().Model(rows).On("CONFLICT (id) DO NOTHING").Exec(ctx)
	return err
}

func firstStr(dst **string, v *string) {
	if *dst == nil && v != nil && *v != "" {
		*dst = v
	}
}

func firstInt(dst **int, v *int) {
	if *dst == nil && v != nil {
		*dst = v
	}
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
