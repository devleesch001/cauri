// Package ingestor reads what Cowrie writes (cowrie.json and the TTY
// recordings) and forwards it, unparsed, to a Sink.
//
// Delivery is at least once: the read position is saved only after the
// Sink acknowledged a batch, so the log files themselves act as the buffer
// while the API is unreachable. The API drops the duplicates a restart may
// send again. TTY recordings are queued, in a persistent file, once the
// batch holding their cowrie.log.closed event is acknowledged.
package ingestor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/devleesch001/cauri/internal/ingestor/tail"
	"github.com/devleesch001/cauri/internal/ttylog"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// LogName is the file Cowrie writes its JSON events to.
const LogName = "cowrie.json"

// maxBatchBytes keeps batches well under protocol.MaxBatchBytes.
const maxBatchBytes = protocol.MaxBatchBytes / 4

// Config configures an ingestor.
type Config struct {
	SensorID string
	LogDir   string // directory of cowrie.json and its rotations
	TTYDir   string // directory of the TTY recordings
	StateDir string // writable directory for the position and the TTY queue
	Sink     Sink

	BatchSize     int           // events per batch at most
	FlushInterval time.Duration // delay before sending an incomplete batch
	PollInterval  time.Duration // delay between checks for new lines
	FromStart     bool          // without saved position, read existing files first

	MinBackoff, MaxBackoff time.Duration // retry delays
	Logger                 *slog.Logger
}

func (c *Config) defaults() error {
	if !protocol.ValidSensorID(c.SensorID) {
		return fmt.Errorf("invalid sensor id %q: use 1 to 64 letters, digits, '.', '_' or '-'", c.SensorID)
	}
	if c.LogDir == "" || c.TTYDir == "" || c.StateDir == "" || c.Sink == nil {
		return errors.New("log, tty and state directories and sink are required")
	}
	if c.BatchSize <= 0 || c.BatchSize > protocol.MaxBatchEvents {
		c.BatchSize = 500
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = time.Second
	}
	if c.MinBackoff <= 0 {
		c.MinBackoff = time.Second
	}
	if c.MaxBackoff < c.MinBackoff {
		c.MaxBackoff = max(time.Minute, c.MinBackoff)
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return nil
}

// Run forwards events and recordings until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.defaults(); err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.StateDir, 0o750); err != nil {
		return err
	}
	in := &ingestor{Config: cfg, posPath: filepath.Join(cfg.StateDir, "position.json")}
	defer func() {
		if in.ttyRoot != nil {
			_ = in.ttyRoot.Close()
		}
	}()

	var saved *tail.Position
	var pos tail.Position
	if ok, err := loadJSON(in.posPath, &pos); err != nil {
		return fmt.Errorf("read position: %w", err)
	} else if ok {
		saved = &pos
	}
	queue, err := loadTTYQueue(filepath.Join(cfg.StateDir, "tty-queue.json"))
	if err != nil {
		return fmt.Errorf("read tty queue: %w", err)
	}
	in.queue = queue
	// Merges left by a stop during an upload: the job is still queued.
	if stale, err := filepath.Glob(filepath.Join(cfg.StateDir, "tty-*.merge")); err == nil {
		for _, f := range stale {
			_ = os.Remove(f)
		}
	}

	follower, err := tail.Open(tail.Options{
		Dir: cfg.LogDir, Name: LogName, Poll: cfg.PollInterval,
		FromStart: cfg.FromStart, Logger: cfg.Logger,
	}, saved)
	if err != nil {
		return err
	}
	defer func() { _ = follower.Close() }()

	cfg.Logger.Info("ingestor started", "sensor", cfg.SensorID, "resume", saved != nil,
		"pending_tty", queue.len())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := make(chan tail.Line, cfg.BatchSize)
	errc := make(chan error, 2)
	go func() { errc <- in.read(ctx, follower, lines) }()
	go func() { errc <- in.uploadTTYs(ctx) }()
	err = in.batch(ctx, lines)
	cancel()
	for range 2 {
		if e := <-errc; err == nil && !errors.Is(e, context.Canceled) {
			err = e
		}
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

type ingestor struct {
	Config
	posPath string
	ttyRoot *os.Root
	queue   *ttyQueue
}

func (in *ingestor) read(ctx context.Context, f *tail.Follower, out chan<- tail.Line) error {
	for {
		l, err := f.Next(ctx)
		if err != nil {
			return err
		}
		select {
		case out <- l:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// batch groups lines and sends them when the batch is full or old enough.
func (in *ingestor) batch(ctx context.Context, lines <-chan tail.Line) error {
	var pending []tail.Line
	size := 0
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	flush := func() error {
		timer.Stop()
		err := in.deliver(ctx, pending)
		pending, size = pending[:0], 0
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case l := <-lines:
			pending = append(pending, l)
			size += len(l.Data)
			if len(pending) == 1 {
				timer.Reset(in.FlushInterval)
			}
			if len(pending) >= in.BatchSize || size >= maxBatchBytes {
				if err := flush(); err != nil {
					return err
				}
			}
		case <-timer.C:
			if err := flush(); err != nil {
				return err
			}
		}
	}
}

// sessionEventOf reads what the TTY queue needs of a Cowrie line: a
// channel's recording (cowrie.log.closed) or the end of its session. A
// field of an unexpected type is ignored, not the event.
func sessionEventOf(raw []byte) (sessionEvent, bool) {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return sessionEvent{}, false
	}
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(m[k], &s)
		return s
	}
	e := sessionEvent{session: str("session")}
	if e.session == "" {
		return e, false
	}
	switch str("eventid") {
	case "cowrie.session.closed":
		return e, true
	case "cowrie.log.closed":
		c := ttyChannel{TTYLog: str("ttylog")}
		if c.TTYLog == "" {
			return e, false
		}
		c.ClosedAt, _ = time.Parse(time.RFC3339Nano, str("timestamp"))
		// Cowrie 3 writes duration_ms, older versions duration in seconds.
		var ms, sec float64
		if json.Unmarshal(m["duration_ms"], &ms) == nil && ms >= 0 && ms < 1e15 {
			c.DurationMS = int64(ms)
		} else if json.Unmarshal(m["duration"], &sec) == nil && sec >= 0 && sec < 1e12 {
			c.DurationMS = int64(sec * 1000)
		}
		_ = json.Unmarshal(m["duplicate"], &c.Duplicate)
		e.channel = &c
		return e, true
	}
	return e, false
}

// deliver sends lines until acknowledged, then queues their recordings and
// saves the position after the last line.
func (in *ingestor) deliver(ctx context.Context, lines []tail.Line) error {
	if len(lines) == 0 {
		return nil
	}
	b := protocol.EventBatch{SensorID: in.SensorID}
	for _, l := range lines {
		if !json.Valid(l.Data) || l.Data[0] != '{' {
			in.Logger.Warn("skipping a line that is not a JSON object", "path", l.Pos.Path, "offset", l.Pos.Offset)
			continue
		}
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID(in.SensorID, l.Data), Raw: l.Data})
	}

	if len(b.Events) > 0 {
		res, err := retry(ctx, in, "send events", func() (protocol.EventBatchResult, error) {
			return in.Sink.SendEvents(ctx, b)
		})
		if err != nil {
			return err
		}
		for _, r := range res.Rejected {
			in.Logger.Warn("event rejected by the api", "id", r.ID, "reason", r.Reason)
		}
		in.Logger.Debug("batch delivered", "accepted", res.Accepted, "duplicates", res.Duplicates, "rejected", len(res.Rejected))

		var events []sessionEvent
		for _, e := range b.Events {
			if se, ok := sessionEventOf(e.Raw); ok {
				events = append(events, se)
			}
		}
		if err := in.queue.record(events, time.Now()); err != nil {
			return fmt.Errorf("save tty queue: %w", err)
		}
	}
	if err := saveJSON(in.posPath, lines[len(lines)-1].Pos); err != nil {
		return fmt.Errorf("save position: %w", err)
	}
	return nil
}

// retry calls f until it succeeds, backing off between attempts. Event
// batches are never dropped: even errors marked permanent are retried, and
// logged, since dropping would lose data silently.
func retry[T any](ctx context.Context, in *ingestor, what string, f func() (T, error)) (T, error) {
	delay := in.MinBackoff
	for {
		v, err := f()
		if err == nil {
			return v, nil
		}
		if ctx.Err() != nil {
			return v, ctx.Err()
		}
		level := slog.LevelWarn
		if errors.Is(err, ErrPermanent) {
			level = slog.LevelError
		}
		in.Logger.Log(ctx, level, what+" failed, retrying", "err", err, "in", delay.String())
		select {
		case <-ctx.Done():
			return v, ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, in.MaxBackoff)
	}
}

// uploadTTYs sends queued recordings, one at a time, as they become due.
func (in *ingestor) uploadTTYs(ctx context.Context) error {
	for {
		job, ok, due := in.queue.next(time.Now())
		if ok && due {
			if err := in.uploadTTY(ctx, job); err != nil {
				return err
			}
			continue
		}
		wait := time.Minute
		if ok {
			wait = time.Until(job.NextTry)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-in.queue.notify:
		case <-time.After(wait):
		}
	}
}

// uploadTTY tries one upload and updates the queue. Only queue persistence
// errors are returned.
func (in *ingestor) uploadTTY(ctx context.Context, job ttyJob) error {
	log := in.Logger.With("session", job.Session, "channels", len(job.Channels))
	if err := in.openTTYRoot(); err != nil {
		// Misconfiguration or directory not created yet: keep the job.
		delay := min(in.MinBackoff<<min(job.Attempts, 16), in.MaxBackoff)
		log.Warn("tty directory unavailable, retrying", "err", err, "in", delay.String())
		return in.queue.retry(job.Session, time.Now().Add(delay))
	}
	f, m, release, err := in.recording(job, log)
	if err != nil {
		log.Warn("tty recording unreadable, dropping it", "err", err)
		return in.queue.done(job.Session)
	}
	res, err := in.Sink.SendTTY(ctx, m, f)
	release()
	if err == nil {
		log.Debug("tty recording delivered", "status", res.Status)
		return in.queue.done(job.Session)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrPermanent) {
		log.Error("tty recording refused by the api, dropping it", "err", err)
		return in.queue.done(job.Session)
	}
	delay := min(in.MinBackoff<<min(job.Attempts, 16), in.MaxBackoff)
	log.Warn("tty upload failed, retrying", "err", err, "in", delay.String())
	return in.queue.retry(job.Session, time.Now().Add(delay))
}

// recording returns the file to upload for a session, at its start, its
// metadata, and how to release it. A session has a recording per SSH
// channel, one per command for many bots: they are merged into one, in
// the order the channels opened. Cowrie keeps one file per input and marks
// later recordings of it duplicate: their file has the times of the
// first, maybe of another session, and is moved to its own channel's
// start. A single recording of its own is sent as Cowrie wrote it.
func (in *ingestor) recording(job ttyJob, log *slog.Logger) (*os.File, protocol.TTYMeta, func(), error) {
	chans := slices.Clone(job.Channels)
	slices.SortStableFunc(chans, func(a, b ttyChannel) int { return a.start().Compare(b.start()) })
	if len(chans) == 1 && !chans[0].Duplicate {
		f, err := in.openTTY(chans[0].TTYLog)
		if err != nil {
			return nil, protocol.TTYMeta{}, nil, err
		}
		m, err := in.describe(job.Session, f)
		if err != nil {
			_ = f.Close()
			return nil, m, nil, err
		}
		return f, m, func() { _ = f.Close() }, nil
	}

	var parts []ttylog.Part
	var opened []*os.File
	defer func() {
		for _, f := range opened {
			_ = f.Close()
		}
	}()
	for _, c := range chans {
		f, err := in.openTTY(c.TTYLog)
		if err != nil {
			log.Warn("channel recording unreadable, leaving it out", "ttylog", c.TTYLog, "err", err)
			continue
		}
		opened = append(opened, f)
		p := ttylog.Part{R: f}
		if c.Duplicate && !c.ClosedAt.IsZero() {
			p.At = c.start()
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return nil, protocol.TTYMeta{}, nil, errors.New("no channel recording readable")
	}
	// Written to a file of the state directory, not kept in memory: a
	// recording can be large.
	tmp, err := os.CreateTemp(in.StateDir, "tty-*.merge")
	if err != nil {
		return nil, protocol.TTYMeta{}, nil, err
	}
	release := func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }
	n, err := io.Copy(tmp, ttylog.Join(parts...))
	if err != nil {
		if n == 0 {
			release()
			return nil, protocol.TTYMeta{}, nil, err
		}
		log.Warn("damaged channel recording, sending what could be read", "err", err)
	}
	m, err := in.describe(job.Session, tmp)
	if err != nil {
		release()
		return nil, m, nil, err
	}
	return tmp, m, release, nil
}

// openTTYRoot opens the TTY directory on first use: Cowrie creates it
// with its first recording, possibly after the ingestor started. Cowrie
// controls its content: os.Root refuses names, symbolic links included,
// that would lead outside of it.
func (in *ingestor) openTTYRoot() error {
	if in.ttyRoot != nil {
		return nil
	}
	root, err := os.OpenRoot(in.TTYDir)
	if err != nil {
		return err
	}
	in.ttyRoot = root
	return nil
}

// openTTY opens a recording written by Cowrie, which must be a regular
// file of the TTY directory.
func (in *ingestor) openTTY(ttyLog string) (*os.File, error) {
	f, err := in.ttyRoot.Open(filepath.Base(ttyLog))
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = errors.New("not a regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// describe hashes a file to upload, from its start, and leaves it at its
// start: the digest lets the API check the upload and recognise one it
// already has.
func (in *ingestor) describe(session string, f *os.File) (protocol.TTYMeta, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return protocol.TTYMeta{}, err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	return protocol.TTYMeta{
		SensorID: in.SensorID, Session: session, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n,
	}, err
}
