// Package ingest is the single entry point for data sent by ingestors:
// validation, deduplication, projection of Cowrie events into tables and
// storage of TTY recordings. HTTP handlers call it; an in-process ingestor
// (all-in-one build) can call it directly.
package ingest

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// Errors returned by the service; callers map them to protocol responses.
var (
	// ErrInvalid wraps every validation error of a request as a whole.
	ErrInvalid = errors.New("invalid request")
	// ErrSensorMismatch means the request names another sensor than the
	// authenticated one.
	ErrSensorMismatch = errors.New("sensor does not match credentials")
	// ErrUnknownSession means a TTY recording arrived for a session whose
	// events have not been ingested yet.
	ErrUnknownSession = errors.New("unknown session")
	// ErrTTYConflict means the session is already linked to another recording.
	ErrTTYConflict = errors.New("session already has another tty recording")
	// ErrDigestMismatch means an upload does not match its digest or size.
	ErrDigestMismatch = ttystore.ErrDigestMismatch
	// ErrTooLarge means a TTY recording exceeds the configured limit.
	ErrTooLarge = errors.New("tty recording too large")
)

// DefaultMaxTTYSize is the default limit on the size of a TTY recording.
const DefaultMaxTTYSize = 128 << 20

// Service ingests events and TTY recordings.
type Service struct {
	db         *bun.DB
	tty        *ttystore.Store
	maxTTYSize int64
	now        func() time.Time
	locator    Locator   // optional
	networker  Networker // optional
}

// Locator locates IP addresses, see package geoip. source names the
// database that answered; ok is false for unknown addresses.
type Locator interface {
	Locate(ip string) (loc geoip.Location, source string, ok bool)
}

// SetLocator makes the service locate new sessions by their source
// address. Without it, sessions are not located.
func (s *Service) SetLocator(l Locator) { s.locator = l }

// Networker finds the autonomous system of IP addresses, see package
// geoip; ok is false for unknown addresses.
type Networker interface {
	Network(ip string) (n geoip.Network, ok bool)
}

// SetNetworker makes the service record the network of new sessions,
// independently of their location: either database may be missing.
func (s *Service) SetNetworker(n Networker) { s.networker = n }

// New returns a service writing to db and tty, refusing TTY recordings
// larger than maxTTYSize bytes (DefaultMaxTTYSize if not positive).
func New(db *bun.DB, tty *ttystore.Store, maxTTYSize int64) *Service {
	if maxTTYSize <= 0 {
		maxTTYSize = DefaultMaxTTYSize
	}
	return &Service{db: db, tty: tty, maxTTYSize: maxTTYSize, now: time.Now}
}

// MaxTTYSize returns the largest TTY recording the service accepts.
func (s *Service) MaxTTYSize() int64 { return s.maxTTYSize }

// IngestEvents stores a batch sent by sensorID, the authenticated sensor.
// Invalid events are rejected one by one; already known events are counted
// as duplicates. The batch is written in one transaction.
func (s *Service) IngestEvents(ctx context.Context, sensorID string, b protocol.EventBatch) (protocol.EventBatchResult, error) {
	var res protocol.EventBatchResult
	if b.SensorID != sensorID {
		return res, ErrSensorMismatch
	}
	if len(b.Events) > protocol.MaxBatchEvents {
		return res, fmt.Errorf("%w: more than %d events", ErrInvalid, protocol.MaxBatchEvents)
	}

	// Validate and drop duplicates inside the batch.
	type parsed struct {
		id  string
		raw string
		ev  *cowrieEvent
	}
	var events []parsed
	seen := map[string]bool{}
	for _, e := range b.Events {
		if !protocol.ValidSHA256(e.ID) {
			res.Rejected = append(res.Rejected, protocol.Rejection{ID: e.ID, Reason: "invalid id"})
			continue
		}
		if seen[e.ID] {
			res.Duplicates++
			continue
		}
		seen[e.ID] = true
		ev, err := parseEvent(e.Raw)
		if err != nil {
			res.Rejected = append(res.Rejected, protocol.Rejection{ID: e.ID, Reason: err.Error()})
			continue
		}
		if ev.Session != "" && !protocol.ValidSession(ev.Session) {
			res.Rejected = append(res.Rejected, protocol.Rejection{ID: e.ID, Reason: "invalid session"})
			continue
		}
		events = append(events, parsed{id: e.ID, raw: strings.ToValidUTF8(string(e.Raw), string(utf8.RuneError)), ev: ev})
	}
	if len(events) == 0 {
		return res, nil
	}

	err := s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// Events already stored do not touch their session again.
		hashes := make([][]byte, len(events))
		for i, e := range events {
			hashes[i], _ = hex.DecodeString(e.id) // checked by ValidSHA256
		}
		var known [][]byte
		if err := tx.NewSelect().Model((*store.Event)(nil)).Column("hash").
			Where("hash IN (?)", bun.List(hashes)).Scan(ctx, &known); err != nil {
			return err
		}
		isKnown := make(map[string]bool, len(known))
		for _, h := range known {
			isKnown[string(h)] = true
		}

		w := newBatchWriter(sensorID, s.now(), s.locator, s.networker)
		for i, e := range events {
			if !isKnown[string(hashes[i])] {
				w.add(hashes[i], e.raw, e.ev)
			}
		}
		accepted, err := w.write(ctx, tx)
		if err != nil {
			return err
		}
		res.Accepted = accepted
		res.Duplicates += len(events) - accepted
		return s.storeVerdict(ctx, tx, w.sessionIDs...)
	})
	if err != nil {
		return protocol.EventBatchResult{}, err
	}
	return res, nil
}

// StoreTTY stores the TTY recording of a session of sensorID, the
// authenticated sensor. Storing the same recording twice is a no-op that
// returns protocol.TTYExists without reading body.
func (s *Service) StoreTTY(ctx context.Context, sensorID string, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error) {
	res := protocol.TTYResult{SHA256: m.SHA256}
	if m.SensorID != sensorID {
		return res, ErrSensorMismatch
	}
	if !protocol.ValidSession(m.Session) || !protocol.ValidSHA256(m.SHA256) || m.Size < 0 {
		return res, fmt.Errorf("%w: bad session, digest or size", ErrInvalid)
	}
	if m.Size > s.maxTTYSize {
		return res, fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, m.Size, s.maxTTYSize)
	}

	var sess struct {
		ID        int64   `bun:"id"`
		TTYSHA256 *string `bun:"tty_sha256"`
	}
	err := s.db.NewSelect().Model((*store.Session)(nil)).
		ColumnExpr("s.id, t.sha256 AS tty_sha256").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id").
		Join("LEFT JOIN tty_files AS t ON t.id = s.tty_file_id").
		Where("sn.name = ?", sensorID).Where("s.cowrie_session = ?", m.Session).Scan(ctx, &sess)
	if errors.Is(err, sql.ErrNoRows) {
		return res, ErrUnknownSession
	}
	if err != nil {
		return res, err
	}
	if sess.TTYSHA256 != nil && *sess.TTYSHA256 != m.SHA256 {
		return res, fmt.Errorf("%w: stored %s", ErrTTYConflict, *sess.TTYSHA256)
	}

	exists, err := s.db.NewSelect().Model((*store.TTYFile)(nil)).Where("sha256 = ?", m.SHA256).Exists(ctx)
	if err != nil {
		return res, err
	}
	res.Status = protocol.TTYExists
	var rel string
	if !exists {
		// Written outside the transaction: a file without a row is
		// harmless, and the next upload of the same file inserts the row.
		if rel, err = s.tty.Write(m.SHA256, m.Size, body); err != nil {
			return res, err
		}
		res.Status = protocol.TTYStored
	}

	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if !exists {
			f := &store.TTYFile{SHA256: m.SHA256, Size: m.Size, Path: rel, StoredAt: store.NewTime(s.now())}
			if _, err := tx.NewInsert().Model(f).On("CONFLICT (sha256) DO NOTHING").Exec(ctx); err != nil {
				return err
			}
		}
		if sess.TTYSHA256 == nil {
			var fileID int64
			if err := tx.NewSelect().Model((*store.TTYFile)(nil)).Column("id").
				Where("sha256 = ?", m.SHA256).Scan(ctx, &fileID); err != nil {
				return err
			}
			_, err := tx.NewUpdate().Model((*store.Session)(nil)).
				Set("tty_file_id = ?", fileID).
				Where("id = ?", sess.ID).Exec(ctx)
			if err != nil {
				return err
			}
			// The recording tells how the keys were typed.
			return s.storeVerdict(ctx, tx, sess.ID)
		}
		return nil
	})
	return res, err
}

// ForgetTTY removes the record of a recording whose file is gone from the
// volume (an operator limiting retention): its sessions no longer have one,
// and are replayed from their commands. Their stored verdict is kept: it
// was judged on the recording, which cannot be read again. A recording
// stored again later is recorded again.
func (s *Service) ForgetTTY(ctx context.Context, sha256 string) error {
	return s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*store.Session)(nil)).
			Set("tty_file_id = NULL").
			Where("tty_file_id IN (SELECT id FROM tty_files WHERE sha256 = ?)", sha256).Exec(ctx)
		if err != nil {
			return err
		}
		_, err = tx.NewDelete().Model((*store.TTYFile)(nil)).Where("sha256 = ?", sha256).Exec(ctx)
		return err
	})
}

// locateBatch is the number of sessions located per transaction.
const locateBatch = 500

// LocateSessions locates the sessions that were stored without a
// location, such as before a GeoIP database was configured, and returns
// how many it looked up. Sessions already located are never changed.
func (s *Service) LocateSessions(ctx context.Context) (int, error) {
	if s.locator == nil {
		return 0, nil
	}
	total := 0
	for {
		var rows []store.Session
		err := s.db.NewSelect().Model(&rows).Column("id", "src_ip").
			Where("geo_source IS NULL").Where("src_ip IS NOT NULL").
			OrderExpr("s.id").Limit(locateBatch).Scan(ctx)
		if err != nil || len(rows) == 0 {
			return total, err
		}
		err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			for _, r := range rows {
				loc, source, ok := s.locator.Locate(*r.SrcIP)
				q := tx.NewUpdate().Model((*store.Session)(nil)).
					Set("geo_source = ?", source).Where("id = ?", r.ID).Where("geo_source IS NULL")
				if ok {
					q = q.Set("geo_country = ?", loc.Country).Set("geo_city = ?", nullable(loc.City))
					if loc.HasCoords {
						q = q.Set("geo_lat = ?", loc.Lat).Set("geo_lon = ?", loc.Lon)
					}
				}
				if _, err := q.Exec(ctx); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += len(rows)
	}
}

// PlaceSessions gives coordinates to the sessions located with a city but
// none, such as before cauri stored them, and returns how many it placed.
// The location of a session is the one of its time: coordinates are only
// taken from the current database where it gives the same country and
// city, so that a point never contradicts the name shown.
func (s *Service) PlaceSessions(ctx context.Context) (int, error) {
	if s.locator == nil {
		return 0, nil
	}
	placed := 0
	var after int64 // sessions left without coordinates are passed once
	for {
		var rows []store.Session
		err := s.db.NewSelect().Model(&rows).Column("id", "src_ip", "geo_country", "geo_city").
			Where("geo_city IS NOT NULL").Where("geo_lat IS NULL").Where("src_ip IS NOT NULL").
			Where("id > ?", after).OrderExpr("s.id").Limit(locateBatch).Scan(ctx)
		if err != nil || len(rows) == 0 {
			return placed, err
		}
		after = rows[len(rows)-1].ID
		err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			for _, r := range rows {
				loc, _, ok := s.locator.Locate(*r.SrcIP)
				if !ok || !loc.HasCoords || loc.Country != *r.GeoCountry || loc.City != *r.GeoCity {
					continue
				}
				if _, err := tx.NewUpdate().Model((*store.Session)(nil)).
					Set("geo_lat = ?", loc.Lat).Set("geo_lon = ?", loc.Lon).
					Where("id = ?", r.ID).Where("geo_lat IS NULL").Exec(ctx); err != nil {
					return err
				}
				placed++
			}
			return nil
		})
		if err != nil {
			return placed, err
		}
	}
}

// NetworkSessions records the network of the sessions stored without one,
// such as before an ASN database was configured, and returns how many it
// found. Addresses the database does not know are passed once per start.
func (s *Service) NetworkSessions(ctx context.Context) (int, error) {
	if s.networker == nil {
		return 0, nil
	}
	found := 0
	var after int64
	for {
		var rows []store.Session
		err := s.db.NewSelect().Model(&rows).Column("id", "src_ip").
			Where("asn IS NULL").Where("src_ip IS NOT NULL").Where("id > ?", after).
			OrderExpr("s.id").Limit(locateBatch).Scan(ctx)
		if err != nil || len(rows) == 0 {
			return found, err
		}
		after = rows[len(rows)-1].ID
		err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			for _, r := range rows {
				n, ok := s.networker.Network(*r.SrcIP)
				if !ok {
					continue
				}
				if _, err := tx.NewUpdate().Model((*store.Session)(nil)).
					Set("asn = ?", int64(n.ASN)).Set("as_org = ?", nullable(n.Org)).
					Where("id = ?", r.ID).Where("asn IS NULL").Exec(ctx); err != nil {
					return err
				}
				found++
			}
			return nil
		})
		if err != nil {
			return found, err
		}
	}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
