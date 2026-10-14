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
	"time"

	"cauri/internal/ingestor/tail"
	"cauri/pkg/protocol"
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

// logClosed holds the fields of cowrie.log.closed the ingestor needs.
type logClosed struct {
	EventID string `json:"eventid"`
	Session string `json:"session"`
	TTYLog  string `json:"ttylog"`
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

		var jobs []ttyJob
		for _, e := range b.Events {
			var lc logClosed
			if json.Unmarshal(e.Raw, &lc) == nil && lc.EventID == "cowrie.log.closed" && lc.TTYLog != "" && lc.Session != "" {
				jobs = append(jobs, ttyJob{Session: lc.Session, TTYLog: lc.TTYLog})
			}
		}
		if err := in.queue.add(jobs...); err != nil {
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
		in.Logger.Log(ctx, level, what+" failed, retrying", "err", err, "in", delay)
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
	log := in.Logger.With("session", job.Session, "ttylog", job.TTYLog)
	// Only the base name of Cowrie's path is kept: the file must be in TTYDir.
	name := filepath.Base(job.TTYLog)
	if name == "." || name == ".." || name == string(filepath.Separator) {
		log.Warn("invalid tty recording name, dropping it")
		return in.queue.done(job.Session)
	}
	path := filepath.Join(in.TTYDir, name)
	m, err := in.ttyMeta(job.Session, path)
	if errors.Is(err, os.ErrNotExist) {
		log.Warn("tty recording missing, dropping it")
		return in.queue.done(job.Session)
	}
	if err == nil {
		var f *os.File
		if f, err = os.Open(path); err == nil { //nolint:gosec // G304: base name inside TTYDir, checked above
			var res protocol.TTYResult
			res, err = in.Sink.SendTTY(ctx, m, f)
			_ = f.Close()
			if err == nil {
				log.Debug("tty recording delivered", "status", res.Status)
				return in.queue.done(job.Session)
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrPermanent) {
		log.Error("tty recording refused by the api, dropping it", "err", err)
		return in.queue.done(job.Session)
	}
	delay := min(in.MinBackoff<<min(job.Attempts, 16), in.MaxBackoff)
	log.Warn("tty upload failed, retrying", "err", err, "in", delay)
	return in.queue.retry(job.Session, time.Now().Add(delay))
}

// ttyMeta hashes a recording: the digest lets the API check the upload
// and recognise one it already has.
func (in *ingestor) ttyMeta(session, path string) (protocol.TTYMeta, error) {
	f, err := os.Open(path) //nolint:gosec // G304: see uploadTTY
	if err != nil {
		return protocol.TTYMeta{}, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return protocol.TTYMeta{}, err
	}
	return protocol.TTYMeta{
		SensorID: in.SensorID, Session: session, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n,
	}, nil
}
