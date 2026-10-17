package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/devleesch001/cauri/internal/cowrie"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttylog"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// TTYFiles yields the files of an upload of a session's channels, one at a
// time: Next returns io.EOF after the last one. A file must be read before
// the next is asked for.
type TTYFiles interface {
	Next() (protocol.TTYFile, error)
}

// FileList yields the files of a slice, for callers that hold them all.
type FileList []protocol.TTYFile

// Next returns the first file of the list and removes it.
func (l *FileList) Next() (protocol.TTYFile, error) {
	if len(*l) == 0 {
		return protocol.TTYFile{}, io.EOF
	}
	f := (*l)[0]
	*l = (*l)[1:]
	return f, nil
}

// StoreTTYChannels stores the recordings of a session's channels as its
// one recording. Cowrie writes a file per SSH channel, and bots run each
// command on its own: the files are merged here, once, by time, each
// placed as its channel's cowrie.log.closed tells. A duplicate (Cowrie's
// file of an earlier run of the same input) is moved to its own channel's
// start, and a file used by several channels of the session is played at
// each; a channel whose file was not sent has no output. A file that is
// no channel's keeps its own times. A single file, its channel's own, is
// stored as Cowrie wrote it.
//
// The same files make the same recording: sending them again changes
// nothing. A session that has another recording keeps it (ErrTTYConflict).
func (s *Service) StoreTTYChannels(ctx context.Context, sensorID, session string, files TTYFiles) (protocol.TTYResult, error) {
	var res protocol.TTYResult
	if !protocol.ValidSession(session) {
		return res, fmt.Errorf("%w: bad session", ErrInvalid)
	}
	var sessionID int64
	err := s.db.NewSelect().Model((*store.Session)(nil)).Column("s.id").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id").
		Where("sn.name = ?", sensorID).Where("s.cowrie_session = ?", session).Scan(ctx, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return res, ErrUnknownSession
	}
	if err != nil {
		return res, err
	}

	received, err := s.receive(files)
	defer received.remove()
	if err != nil {
		return res, err
	}
	channels, err := s.sessionChannels(ctx, sessionID)
	if err != nil {
		return res, err
	}
	merged, size, sha, err := s.merge(received, channels)
	if merged != nil {
		defer func() {
			_ = merged.Close()
			_ = os.Remove(merged.Name()) // a work file, or a received one
		}()
	}
	if err != nil {
		return res, err
	}
	return s.StoreTTY(ctx, sensorID, protocol.TTYMeta{SensorID: sensorID, Session: session, SHA256: sha, Size: size},
		io.NewSectionReader(merged, 0, size))
}

// receivedFile is a file of an upload, checked and kept for the merge.
type receivedFile struct {
	f    *os.File
	size int64
	sha  string
	name string // Cowrie's, from its input; empty if unreadable
}

type receivedFiles []receivedFile

func (r receivedFiles) remove() {
	for _, f := range r {
		_ = f.f.Close()
		_ = os.Remove(f.f.Name())
	}
}

// receive writes the files of an upload to the store's work directory,
// checking each against its digest and all against the size limit. The
// same file sent twice is kept once.
func (s *Service) receive(files TTYFiles) (receivedFiles, error) {
	var out receivedFiles
	var total int64
	for {
		f, err := files.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return out, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if len(out) == protocol.MaxTTYChannels {
			return out, fmt.Errorf("%w: more than %d files", ErrInvalid, protocol.MaxTTYChannels)
		}
		if !protocol.ValidSHA256(f.SHA256) || f.Size < 0 {
			return out, fmt.Errorf("%w: bad digest or size", ErrInvalid)
		}
		if total += f.Size; total > s.maxTTYSize {
			return out, fmt.Errorf("%w: over %d bytes", ErrTooLarge, s.maxTTYSize)
		}
		tmp, err := s.tty.Temp()
		if err != nil {
			return out, err
		}
		rf := receivedFile{f: tmp, size: f.Size, sha: f.SHA256}
		out = append(out, rf)
		h := sha256.New()
		// One byte more than announced, to detect oversized bodies.
		n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(f.Body, f.Size+1))
		if err != nil {
			return out, err
		}
		if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
			return out, ErrDigestMismatch
		}
		if slices.ContainsFunc(out[:len(out)-1], func(o receivedFile) bool { return o.sha == f.SHA256 }) {
			continue // removed with the others
		}
		if name, err := ttylog.Name(io.NewSectionReader(tmp, 0, f.Size)); err == nil {
			out[len(out)-1].name = name
		}
	}
	if len(out) == 0 {
		return out, fmt.Errorf("%w: no file", ErrInvalid)
	}
	return out, nil
}

// channel is an SSH channel of a session, as its cowrie.log.closed told.
type channel struct {
	start     time.Time
	name      string
	duplicate bool
}

// sessionChannels returns the channels of a session in the order they
// started. Their lines are read from the raw line while it is kept, else
// from the event's data, which holds them.
func (s *Service) sessionChannels(ctx context.Context, sessionID int64) ([]channel, error) {
	var lines []struct {
		TS   store.Time `bun:"ts"`
		Data *string    `bun:"data"`
	}
	if err := s.db.NewSelect().Model((*store.Event)(nil)).ColumnExpr("e.ts, COALESCE(ed.data, e.data) AS data").
		Join("LEFT JOIN event_raw_data AS ed ON ed.event_id = e.id").
		Where("e.session_id = ?", sessionID).Where("e.type = ?", evLogClosed).
		OrderExpr("e.ts, e.id").Scan(ctx, &lines); err != nil {
		return nil, err
	}
	out := make([]channel, 0, len(lines))
	for _, l := range lines {
		c := channel{start: l.TS.Time}
		if l.Data != nil {
			if f, ok := cowrie.Parse([]byte(*l.Data)); ok {
				p := cowrie.ParseChannel(f)
				if p.TTYLog != nil {
					c.name = *p.TTYLog
				}
				if p.DurationMS != nil {
					c.start = c.start.Add(-time.Duration(*p.DurationMS) * time.Millisecond)
				}
				c.duplicate = p.Duplicate
			}
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b channel) int { return a.start.Compare(b.start) })
	return out, nil
}

// merge writes the session's recording from its files, and returns it
// with its size and digest. A single file of its own channel is returned
// as it is.
func (s *Service) merge(files receivedFiles, channels []channel) (*os.File, int64, string, error) {
	byName := map[string]*receivedFile{}
	for i := range files {
		if n := files[i].name; n != "" && byName[n] == nil {
			byName[n] = &files[i]
		}
	}
	var parts []ttylog.Part
	var only *receivedFile // the single part, if unmoved
	used := map[string]bool{}
	for _, c := range channels {
		f := byName[c.name]
		if f == nil {
			continue
		}
		used[f.sha] = true
		part := ttylog.Part{R: io.NewSectionReader(f.f, 0, f.size)}
		if c.duplicate {
			part.At = c.start
		}
		parts = append(parts, part)
		if !c.duplicate {
			only = f
		}
	}
	// Files no channel names, in digest order so that the same files
	// make the same recording.
	rest := slices.Clone(files)
	slices.SortFunc(rest, func(a, b receivedFile) int { return strings.Compare(a.sha, b.sha) })
	for i := range rest {
		if !used[rest[i].sha] {
			parts = append(parts, ttylog.Part{R: io.NewSectionReader(rest[i].f, 0, rest[i].size)})
			only = &rest[i]
		}
	}
	if len(parts) == 1 && only != nil {
		f, err := os.Open(only.f.Name())
		if err != nil {
			return nil, 0, "", err
		}
		return f, only.size, only.sha, nil
	}

	out, err := s.tty.Temp()
	if err != nil {
		return nil, 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(ttylog.Join(parts...), s.maxTTYSize+1))
	// A damaged file ends its part: the others are merged, and the same
	// files still make the same recording.
	if err != nil && !errors.Is(err, ttylog.ErrTruncated) && !errors.Is(err, ttylog.ErrCorrupt) {
		return out, 0, "", err
	}
	if n > s.maxTTYSize {
		return out, 0, "", fmt.Errorf("%w: merged over %d bytes", ErrTooLarge, s.maxTTYSize)
	}
	if n == 0 {
		return out, 0, "", fmt.Errorf("%w: no readable recording", ErrInvalid)
	}
	return out, n, hex.EncodeToString(h.Sum(nil)), nil
}
