package ingest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/cowrie"
	"github.com/devleesch001/cauri/internal/store"
)

// batchWriter accumulates the new events of a batch and the sessions
// they belong to, then writes them (see write).
//
// Sessions are merged with rules that do not depend on arrival order, in
// memory within a batch and in SQL across batches (see upsertSessions):
// connection details keep their first value, started_at the earliest
// timestamp, end and recording details their last value, login_success
// becomes true once any login succeeded. The recording file is only set by
// StoreTTY, from the uploaded file.
type batchWriter struct {
	now       store.Time
	locator   Locator   // optional
	networker Networker // optional
	sensor    store.Sensor
	sessions  map[string]*store.Session // by Cowrie session id
	order     []string                  // Cowrie session ids, in order of appearance
	noRawData bool                      // see Service.SetRawData
	kex       map[string]*cowrie.Kex    // the first key exchange of each session
	geo       map[string]int64          // GeoIP database ids, by geoip.Info.Source
	networks  map[uint32]int64          // network ids, by ASN
	events    []pendingEvent
	// sessionIDs are the sessions written, once write returned.
	sessionIDs []int64
}

// pendingEvent is a Cowrie line not stored yet.
type pendingEvent struct {
	hash []byte
	raw  string
	ev   *cowrieEvent
}

func newBatchWriter(sensor string, now time.Time, locator Locator, networker Networker) *batchWriter {
	return &batchWriter{
		now:       store.NewTime(now),
		locator:   locator,
		networker: networker,
		sensor:    store.Sensor{Name: sensor},
		sessions:  map[string]*store.Session{},
		kex:       map[string]*cowrie.Kex{},
		geo:       map[string]int64{},
		networks:  map[uint32]int64{},
	}
}

func (w *batchWriter) add(hash []byte, raw string, ev *cowrieEvent) {
	ts := store.NewTime(ev.ts)
	if w.sensor.FirstSeen == nil || ts.Before(w.sensor.FirstSeen.Time) {
		w.sensor.FirstSeen = &ts
	}
	if w.sensor.LastSeen == nil || ts.After(w.sensor.LastSeen.Time) {
		w.sensor.LastSeen = &ts
	}
	if ev.Session != "" {
		w.merge(w.session(ev), ev)
	}
	w.events = append(w.events, pendingEvent{hash: hash, raw: raw, ev: ev})
}

// session returns the pending session row of ev, created on first use.
func (w *batchWriter) session(ev *cowrieEvent) *store.Session {
	ts := store.NewTime(ev.ts)
	s, ok := w.sessions[ev.Session]
	if !ok {
		s = &store.Session{CowrieSession: ev.Session, StartedAt: ts}
		w.sessions[ev.Session] = s
		w.order = append(w.order, ev.Session)
	}
	if ts.Before(s.StartedAt.Time) {
		s.StartedAt = ts
	}
	firstStr(&s.SrcIP, ev.SrcIP)
	return s
}

// merge turns a known event into session fields.
func (w *batchWriter) merge(s *store.Session, ev *cowrieEvent) {
	switch ev.EventID {
	case evConnect:
		firstInt(&s.SrcPort, ev.SrcPort)
		firstStr(&s.DstIP, ev.DstIP)
		firstInt(&s.DstPort, ev.DstPort)
		firstStr(&s.Protocol, ev.Protocol)
	case evClientVersion:
		firstStr(&s.ClientVersion, ev.Version)
	case evClientKex:
		if _, ok := w.kex[ev.Session]; !ok && ev.Kex != nil {
			w.kex[ev.Session] = ev.Kex
		}
	case evLoginSuccess:
		s.LoginSuccess = true
	case evLogClosed:
		if name := ev.ttyName(); name != nil {
			s.TTYLog = name
		}
		if d := ev.durationMS; d != nil {
			s.TTYDurationMS = d
		}
	case evSessionClosed:
		ts := store.NewTime(ev.ts)
		s.EndedAt = &ts
		if d := ev.durationMS; d != nil {
			s.DurationMS = d
		}
	}
}

// rows are the table rows of a known event of a session.
type rows struct {
	logins   []store.LoginAttempt
	commands []store.Command
	xfers    []store.Transfer
}

func (r *rows) add(sessionID int64, ev *cowrieEvent) {
	ts := store.NewTime(ev.ts)
	switch ev.EventID {
	case evLoginSuccess, evLoginFailed:
		r.logins = append(r.logins, store.LoginAttempt{
			SessionID: sessionID, TS: ts,
			Username: deref(ev.Username), Password: deref(ev.Password), Success: ev.EventID == evLoginSuccess,
		})
	case evCommandInput:
		r.commands = append(r.commands, store.Command{SessionID: sessionID, TS: ts, Input: deref(ev.Input)})
	case evFileDownload:
		r.xfers = append(r.xfers, store.Transfer{
			SessionID: sessionID, TS: ts, Direction: store.Download,
			URL: ev.URL, Filename: ev.DestFile, SHA256: ev.Shasum,
		})
	case evFileUpload:
		r.xfers = append(r.xfers, store.Transfer{
			SessionID: sessionID, TS: ts, Direction: store.Upload,
			Filename: ev.Filename, SHA256: ev.Shasum,
		})
	}
}

// write stores the batch and returns how many events were new. Events are
// inserted with ON CONFLICT DO NOTHING: only the lines no other batch
// stored come back, and only those get their raw data and table rows, so
// a batch sent twice, even concurrently, is stored once.
func (w *batchWriter) write(ctx context.Context, tx bun.Tx) (int, error) {
	if err := tx.NewInsert().Model(&w.sensor).
		On("CONFLICT (name) DO UPDATE").
		Set("first_seen = CASE WHEN ?TableAlias.first_seen IS NULL OR EXCLUDED.first_seen < ?TableAlias.first_seen THEN EXCLUDED.first_seen ELSE ?TableAlias.first_seen END").
		Set("last_seen = CASE WHEN ?TableAlias.last_seen IS NULL OR EXCLUDED.last_seen > ?TableAlias.last_seen THEN EXCLUDED.last_seen ELSE ?TableAlias.last_seen END").
		Returning("id").Scan(ctx); err != nil {
		return 0, fmt.Errorf("sensor: %w", err)
	}
	sessionIDs, err := w.upsertSessions(ctx, tx)
	if err != nil {
		return 0, fmt.Errorf("sessions: %w", err)
	}
	if len(w.events) == 0 {
		return 0, nil
	}

	events := make([]store.Event, len(w.events))
	for i, p := range w.events {
		events[i] = store.Event{
			Hash: p.hash, SensorID: w.sensor.ID, Type: p.ev.EventID,
			TS: store.NewTime(p.ev.ts), ReceivedAt: w.now, Data: p.ev.data,
		}
		if id, ok := sessionIDs[p.ev.Session]; ok {
			events[i].SessionID = &id
		}
	}
	var stored []struct {
		ID   int64  `bun:"id"`
		Hash []byte `bun:"hash"`
	}
	if err := tx.NewInsert().Model(&events).On("CONFLICT (hash) DO NOTHING").
		Returning("id, hash").Scan(ctx, &stored); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("events: %w", err)
	}
	if len(stored) == 0 {
		return 0, nil
	}
	ids := make(map[string]int64, len(stored))
	for _, e := range stored {
		ids[string(e.Hash)] = e.ID
	}

	data := make([]store.EventRawData, 0, len(stored))
	var r rows
	for _, p := range w.events {
		id, ok := ids[string(p.hash)]
		if !ok {
			continue // stored by another batch meanwhile
		}
		if !w.noRawData {
			data = append(data, store.EventRawData{EventID: id, Data: p.raw})
		}
		if sid, ok := sessionIDs[p.ev.Session]; ok {
			r.add(sid, p.ev)
		}
	}
	if err := insert(ctx, tx, &data); err != nil {
		return 0, fmt.Errorf("event data: %w", err)
	}
	if err := insert(ctx, tx, &r.logins); err != nil {
		return 0, fmt.Errorf("login attempts: %w", err)
	}
	if err := insert(ctx, tx, &r.commands); err != nil {
		return 0, fmt.Errorf("commands: %w", err)
	}
	if err := insert(ctx, tx, &r.xfers); err != nil {
		return 0, fmt.Errorf("transfers: %w", err)
	}
	return len(stored), nil
}

// upsertSessions writes the sessions of the batch and returns their ids
// by Cowrie session id.
func (w *batchWriter) upsertSessions(ctx context.Context, tx bun.Tx) (map[string]int64, error) {
	ids := map[string]int64{}
	if len(w.order) == 0 {
		return ids, nil
	}
	kexIDs, err := w.upsertKex(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("key exchanges: %w", err)
	}
	rows := make([]store.Session, len(w.order))
	for i, cs := range w.order {
		rows[i] = *w.sessions[cs]
		rows[i].SensorID = w.sensor.ID
		if k, ok := w.kex[cs]; ok {
			id := kexIDs[string(k.Digest)]
			rows[i].SSHKexID = &id
		}
		if err := w.locate(ctx, tx, &rows[i]); err != nil {
			return nil, fmt.Errorf("location: %w", err)
		}
		if err := w.network(ctx, tx, &rows[i]); err != nil {
			return nil, fmt.Errorf("network: %w", err)
		}
	}
	q := tx.NewInsert().Model(&rows).On("CONFLICT (sensor_id, cowrie_session) DO UPDATE").
		Set("started_at = CASE WHEN EXCLUDED.started_at < ?TableAlias.started_at THEN EXCLUDED.started_at ELSE ?TableAlias.started_at END").
		Set("login_success = (?TableAlias.login_success OR EXCLUDED.login_success)")
	for _, col := range []string{"src_ip", "src_port", "dst_ip", "dst_port", "protocol", "client_version", "ssh_kex_id", "network_id"} {
		q = q.Set(col + " = COALESCE(?TableAlias." + col + ", EXCLUDED." + col + ")")
	}
	for _, col := range []string{"ended_at", "duration_ms", "tty_log", "tty_duration_ms"} {
		q = q.Set(col + " = COALESCE(EXCLUDED." + col + ", ?TableAlias." + col + ")")
	}
	// A located session keeps its location: the columns come from the same
	// lookup, the first one.
	for _, col := range []string{"geo_country", "geo_city", "geo_lat", "geo_lon", "geo_source_id"} {
		q = q.Set(col + " = CASE WHEN ?TableAlias.geo_source_id IS NULL THEN EXCLUDED." + col + " ELSE ?TableAlias." + col + " END")
	}
	var out []struct {
		ID            int64  `bun:"id"`
		CowrieSession string `bun:"cowrie_session"`
	}
	if err := q.Returning("id, cowrie_session").Scan(ctx, &out); err != nil {
		return nil, err
	}
	for _, o := range out {
		ids[o.CowrieSession] = o.ID
		w.sessionIDs = append(w.sessionIDs, o.ID)
	}
	return ids, nil
}

// upsertKex stores the key exchanges of the batch, once per combination,
// and returns their ids by digest.
func (w *batchWriter) upsertKex(ctx context.Context, tx bun.Tx) (map[string]int64, error) {
	ids := map[string]int64{}
	if len(w.kex) == 0 {
		return ids, nil
	}
	var rows []store.SSHKex
	var digests [][]byte
	listed := map[string]bool{}
	for _, cs := range w.order {
		k, ok := w.kex[cs]
		if !ok || listed[string(k.Digest)] {
			continue
		}
		listed[string(k.Digest)] = true
		rows = append(rows, store.NewSSHKex(*k))
		digests = append(digests, k.Digest)
	}
	if _, err := tx.NewInsert().Model(&rows).On("CONFLICT (digest) DO NOTHING").Returning("NULL").Exec(ctx); err != nil {
		return nil, err
	}
	var found []store.SSHKex
	if err := tx.NewSelect().Model(&found).Column("id", "digest").Where("digest IN (?)", bun.List(digests)).Scan(ctx); err != nil {
		return nil, err
	}
	for _, k := range found {
		ids[string(k.Digest)] = k.ID
	}
	return ids, nil
}

// network sets the autonomous system of a session row from its source
// address.
func (w *batchWriter) network(ctx context.Context, tx bun.Tx, s *store.Session) error {
	if w.networker == nil || s.SrcIP == nil {
		return nil
	}
	n, ok := w.networker.Network(*s.SrcIP)
	if !ok {
		return nil
	}
	id, known := w.networks[n.ASN]
	if !known {
		var err error
		if id, err = store.NetworkID(ctx, tx, int64(n.ASN), nullable(n.Org)); err != nil {
			return err
		}
		w.networks[n.ASN] = id
	}
	s.NetworkID = &id
	return nil
}

// locate sets the location of a session row from its source address,
// and the database that answered, even if it did not know the address.
func (w *batchWriter) locate(ctx context.Context, tx bun.Tx, s *store.Session) error {
	if w.locator == nil || s.SrcIP == nil {
		return nil
	}
	loc, source, ok := w.locator.Locate(*s.SrcIP)
	id, known := w.geo[source.Source()]
	if !known {
		var err error
		if id, err = store.GeoSourceID(ctx, tx, source.Type, source.Built); err != nil {
			return err
		}
		w.geo[source.Source()] = id
	}
	s.GeoSourceID = &id
	if ok {
		s.GeoCountry = &loc.Country
		s.GeoCity = nullable(loc.City)
		if loc.HasCoords {
			s.GeoLat, s.GeoLon = &loc.Lat, &loc.Lon
		}
	}
	return nil
}

func insert[T any](ctx context.Context, tx bun.Tx, rows *[]T) error {
	if len(*rows) == 0 {
		return nil
	}
	_, err := tx.NewInsert().Model(rows).Exec(ctx)
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
