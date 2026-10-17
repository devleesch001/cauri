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
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devleesch001/cauri/pkg/protocol"
)

// fakeSink records what it receives; failEvents/failTTY make the next
// calls fail with the given error.
type fakeSink struct {
	mu         sync.Mutex
	events     []protocol.Event
	ttys       map[string]string // session -> content
	ttyMeta    map[string]protocol.TTYMeta
	channels   map[string][]string // session -> contents of its channels' files
	failEvents int
	failTTY    int
	ttyErr     error
	calls      int
}

func newFakeSink() *fakeSink {
	return &fakeSink{ttys: map[string]string{}, ttyMeta: map[string]protocol.TTYMeta{}, channels: map[string][]string{}}
}

func (s *fakeSink) SendEvents(_ context.Context, b protocol.EventBatch) (protocol.EventBatchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failEvents > 0 {
		s.failEvents--
		return protocol.EventBatchResult{}, errors.New("api unreachable")
	}
	for _, e := range b.Events {
		if b.SensorID != "hp-01" || e.ID != protocol.EventID("hp-01", e.Raw) {
			panic("bad batch")
		}
	}
	s.events = append(s.events, b.Events...)
	return protocol.EventBatchResult{Accepted: len(b.Events)}, nil
}

func (s *fakeSink) SendTTY(_ context.Context, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failTTY > 0 {
		s.failTTY--
		return protocol.TTYResult{}, s.ttyErr
	}
	data, _ := io.ReadAll(body)
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != m.SHA256 || int64(len(data)) != m.Size {
		panic("digest or size mismatch")
	}
	s.ttys[m.Session] = string(data)
	s.ttyMeta[m.Session] = m
	return protocol.TTYResult{Status: protocol.TTYStored, SHA256: m.SHA256}, nil
}

func (s *fakeSink) SendTTYChannels(_ context.Context, sensorID, session string, files []protocol.TTYFile) (protocol.TTYResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failTTY > 0 {
		s.failTTY--
		return protocol.TTYResult{}, s.ttyErr
	}
	if sensorID != "hp-01" {
		panic("bad sensor")
	}
	var contents []string
	for _, f := range files {
		data, _ := io.ReadAll(f.Body)
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != f.SHA256 || int64(len(data)) != f.Size {
			panic("digest or size mismatch")
		}
		contents = append(contents, string(data))
	}
	s.channels[session] = contents
	return protocol.TTYResult{Status: protocol.TTYStored}, nil
}

func (s *fakeSink) sentChannels() map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.channels)
}

func (s *fakeSink) snapshot() ([]protocol.Event, map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ttys := map[string]string{}
	for k, v := range s.ttys {
		ttys[k] = v
	}
	return append([]protocol.Event(nil), s.events...), ttys
}

type fixture struct {
	logDir, ttyDir, stateDir string
	sink                     *fakeSink
}

func newFixture(t *testing.T) *fixture {
	root := t.TempDir()
	f := &fixture{
		logDir: filepath.Join(root, "log"), ttyDir: filepath.Join(root, "tty"),
		stateDir: filepath.Join(root, "state"), sink: newFakeSink(),
	}
	for _, d := range []string{f.logDir, f.ttyDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fixture) config(sink Sink) Config {
	return Config{
		SensorID: "hp-01", LogDir: f.logDir, TTYDir: f.ttyDir, StateDir: f.stateDir, Sink: sink,
		BatchSize: 3, FlushInterval: 10 * time.Millisecond, PollInterval: 5 * time.Millisecond,
		FromStart: true, MinBackoff: 5 * time.Millisecond, MaxBackoff: 20 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// start runs the ingestor in the background; the returned function stops
// it and waits for it.
func (f *fixture) start(t *testing.T, cfg Config) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("Run: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func (f *fixture) write(t *testing.T, name string, lines ...string) {
	t.Helper()
	fh, err := os.OpenFile(filepath.Join(f.logDir, name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		_, _ = fh.WriteString(l + "\n")
	}
	_ = fh.Close()
}

func event(n int) string {
	return fmt.Sprintf(`{"eventid":"cowrie.command.input","session":"s%d","timestamp":"2026-10-14T09:00:%02d.000000Z","input":"cmd %d"}`, n, n%60, n)
}

// sessionClosedEvent ends a session: its recordings can be sent.
func sessionClosedEvent(session string) string {
	return fmt.Sprintf(`{"eventid":"cowrie.session.closed","session":%q,"timestamp":"2026-10-14T09:59:30.000000Z","duration_ms":1000}`, session)
}

func logClosedEvent(session, ttyFile string) string {
	return fmt.Sprintf(`{"eventid":"cowrie.log.closed","session":%q,"timestamp":"2026-10-14T09:59:00.000000Z","ttylog":"var/lib/cowrie/tty/%s"}`, session, ttyFile)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func eventCount(s *fakeSink) func() int {
	return func() int { e, _ := s.snapshot(); return len(e) }
}

// inputs returns the "input" field of received events, in order.
func inputs(events []protocol.Event) []string {
	var out []string
	for _, e := range events {
		var v struct{ Input string }
		_ = json.Unmarshal(e.Raw, &v)
		out = append(out, v.Input)
	}
	return out
}

func TestEventsAndRotation(t *testing.T) {
	f := newFixture(t)
	f.write(t, LogName, event(1), event(2))
	f.start(t, f.config(f.sink))
	waitFor(t, "first events", func() bool { return eventCount(f.sink)() == 2 })

	if err := os.Rename(filepath.Join(f.logDir, LogName), filepath.Join(f.logDir, LogName+".2026-10-14")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	f.write(t, LogName, event(3), "not json", event(4))
	waitFor(t, "events after rotation", func() bool { return eventCount(f.sink)() == 4 })

	events, _ := f.sink.snapshot()
	if got := fmt.Sprint(inputs(events)); got != "[cmd 1 cmd 2 cmd 3 cmd 4]" {
		t.Fatalf("events = %s", got)
	}
}

// After a restart, reading resumes after the last acknowledged line.
func TestResumeAfterRestart(t *testing.T) {
	f := newFixture(t)
	f.write(t, LogName, event(1), event(2), event(3), event(4))
	stop := f.start(t, f.config(f.sink))
	waitFor(t, "events", func() bool { return eventCount(f.sink)() == 4 })
	stop()

	f.write(t, LogName, event(5))
	f.start(t, f.config(f.sink))
	waitFor(t, "new event", func() bool { return eventCount(f.sink)() >= 5 })
	time.Sleep(50 * time.Millisecond)
	events, _ := f.sink.snapshot()
	if got := fmt.Sprint(inputs(events)); got != "[cmd 1 cmd 2 cmd 3 cmd 4 cmd 5]" {
		t.Fatalf("events = %s, want no resend", got)
	}
}

// While the API is down nothing is lost, and the position does not move.
func TestAPIDown(t *testing.T) {
	f := newFixture(t)
	f.sink.failEvents = 1 << 30
	f.write(t, LogName, event(1), event(2))
	stop := f.start(t, f.config(f.sink))
	waitFor(t, "send attempts", func() bool { f.sink.mu.Lock(); defer f.sink.mu.Unlock(); return f.sink.calls >= 3 })
	stop()
	if _, err := os.Stat(filepath.Join(f.stateDir, "position.json")); !os.IsNotExist(err) {
		t.Fatalf("position saved without acknowledgement: %v", err)
	}

	// Rotation while down, then the API comes back.
	if err := os.Rename(filepath.Join(f.logDir, LogName), filepath.Join(f.logDir, LogName+".2026-10-14")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	f.write(t, LogName, event(3))
	f.sink.mu.Lock()
	f.sink.failEvents = 2
	f.sink.mu.Unlock()
	f.start(t, f.config(f.sink))
	waitFor(t, "all events", func() bool { return eventCount(f.sink)() == 3 })
}

// Without saved position, the same lines get the same ids: the API can
// drop what it already has.
func TestStableIDs(t *testing.T) {
	f := newFixture(t)
	f.write(t, LogName, event(1), event(2))
	stop := f.start(t, f.config(f.sink))
	waitFor(t, "events", func() bool { return eventCount(f.sink)() == 2 })
	stop()
	if err := os.Remove(filepath.Join(f.stateDir, "position.json")); err != nil {
		t.Fatal(err)
	}
	f.start(t, f.config(f.sink))
	waitFor(t, "resent events", func() bool { return eventCount(f.sink)() == 4 })
	events, _ := f.sink.snapshot()
	if events[0].ID != events[2].ID || events[1].ID != events[3].ID || events[0].ID == events[1].ID {
		t.Fatal("ids are not stable per line")
	}
}

func writeTTY(t *testing.T, f *fixture, content string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(content))
	name := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(f.ttyDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestTTYUpload(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "\x1b[0mroot@svr04:~# ls\r\n")
	f.write(t, LogName, event(1), logClosedEvent("s1", tty), logClosedEvent("s2", "missing"),
		sessionClosedEvent("s1"), sessionClosedEvent("s2"))
	f.start(t, f.config(f.sink))
	waitFor(t, "tty upload", func() bool { _, ttys := f.sink.snapshot(); return len(ttys) == 1 })
	_, ttys := f.sink.snapshot()
	if ttys["s1"] != "\x1b[0mroot@svr04:~# ls\r\n" || f.sink.ttyMeta["s1"].SensorID != "hp-01" {
		t.Fatalf("ttys = %q", ttys)
	}
	q, _ := loadTTYQueue(filepath.Join(f.stateDir, "tty-queue.json"))
	waitFor(t, "empty queue", func() bool { q, _ = loadTTYQueue(q.path); return q.len() == 0 })
}

// A failed upload stays queued across restarts and is retried.
func TestTTYRetryAcrossRestart(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "output")
	f.sink.failTTY = 1 << 30
	f.sink.ttyErr = errors.New("api unreachable")
	f.write(t, LogName, logClosedEvent("s1", tty), sessionClosedEvent("s1"))
	stop := f.start(t, f.config(f.sink))
	waitFor(t, "events", func() bool { return eventCount(f.sink)() == 2 })
	q, _ := loadTTYQueue(filepath.Join(f.stateDir, "tty-queue.json"))
	waitFor(t, "a failed attempt", func() bool {
		q, _ = loadTTYQueue(q.path)
		j, ok, _ := q.next(time.Now())
		return ok && j.Attempts > 0
	})
	stop()

	f.sink.mu.Lock()
	f.sink.failTTY = 0
	f.sink.mu.Unlock()
	f.start(t, f.config(f.sink))
	waitFor(t, "tty upload after restart", func() bool { _, ttys := f.sink.snapshot(); return ttys["s1"] == "output" })
}

// Permanent refusals (conflict, digest mismatch) are not retried forever.
func TestTTYPermanentError(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "output")
	f.sink.failTTY = 1
	f.sink.ttyErr = fmt.Errorf("%w: conflict", ErrPermanent)
	f.write(t, LogName, logClosedEvent("s1", tty), sessionClosedEvent("s1"))
	f.start(t, f.config(f.sink))
	q, _ := loadTTYQueue(filepath.Join(f.stateDir, "tty-queue.json"))
	waitFor(t, "dropped job", func() bool {
		q, _ = loadTTYQueue(q.path)
		f.sink.mu.Lock()
		defer f.sink.mu.Unlock()
		return f.sink.failTTY == 0 && q.len() == 0
	})
	if _, ttys := f.sink.snapshot(); len(ttys) != 0 {
		t.Fatalf("permanent error retried: %v", ttys)
	}
}

func TestConfigValidation(t *testing.T) {
	f := newFixture(t)
	cfg := f.config(f.sink)
	cfg.SensorID = "bad/id"
	if err := Run(context.Background(), cfg); err == nil {
		t.Fatal("want error for invalid sensor id")
	}
}

// A compromised Cowrie must not make the ingestor upload files from
// outside the TTY directory.
func TestTTYOutsideDirectory(t *testing.T) {
	f := newFixture(t)
	secret := filepath.Join(filepath.Dir(f.ttyDir), "secret")
	if err := os.WriteFile(secret, []byte("token"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(f.ttyDir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.ttyDir, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	f.write(t, LogName,
		logClosedEvent("s1", "link"),
		logClosedEvent("s2", "../secret"), // base name only: "secret", absent from the directory
		logClosedEvent("s3", "dir"),
		sessionClosedEvent("s1"), sessionClosedEvent("s2"), sessionClosedEvent("s3"),
	)
	f.start(t, f.config(f.sink))
	q, _ := loadTTYQueue(filepath.Join(f.stateDir, "tty-queue.json"))
	waitFor(t, "jobs dropped", func() bool {
		q, _ = loadTTYQueue(q.path)
		return eventCount(f.sink)() == 6 && q.len() == 0
	})
	if _, ttys := f.sink.snapshot(); len(ttys) != 0 {
		t.Fatalf("uploaded %v", ttys)
	}
}

// On a fresh Cowrie, its log and tty directories appear with the first
// connection, possibly after the ingestor started.
func TestDirectoriesCreatedLater(t *testing.T) {
	f := newFixture(t)
	for _, d := range []string{f.logDir, f.ttyDir} {
		if err := os.Remove(d); err != nil {
			t.Fatal(err)
		}
	}
	f.start(t, f.config(f.sink))
	time.Sleep(30 * time.Millisecond)

	for _, d := range []string{f.logDir, f.ttyDir} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	tty := writeTTY(t, f, "output")
	f.write(t, LogName, event(1), logClosedEvent("s1", tty), sessionClosedEvent("s1"))
	waitFor(t, "events and recording", func() bool {
		_, ttys := f.sink.snapshot()
		return eventCount(f.sink)() == 3 && ttys["s1"] == "output"
	})
}

// Bots run one command per exec channel: Cowrie writes a recording per
// channel, and keeps one file per input (a later one is a duplicate, with
// the times of the first). Every file of such a session is sent at once,
// each once, for the API to merge (sample of the official Cowrie v3.1.2,
// see internal/ingest).
func TestTTYSeveralChannelsSent(t *testing.T) {
	f := newFixture(t)
	sample := "../ingest/testdata/cowrie-v3.1.2-exec"
	files, err := filepath.Glob(sample + "-tty/*")
	if err != nil || len(files) != 3 {
		t.Fatalf("sample files: %v %v", files, err)
	}
	for _, p := range files {
		data, _ := os.ReadFile(p)
		if err := os.WriteFile(filepath.Join(f.ttyDir, filepath.Base(p)), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cowrieLog, err := os.ReadFile(sample + ".json")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(cowrieLog)), "\n")
	f.write(t, LogName, lines...)
	f.start(t, f.config(f.sink))
	waitFor(t, "uploads", func() bool { return len(f.sink.sentChannels()) == 2 })
	if _, ttys := f.sink.snapshot(); len(ttys) != 0 {
		t.Errorf("single recordings sent for sessions with several channels: %v", ttys)
	}
	// Each file once: uname -a ran twice in the first session, its second
	// run a duplicate; the second session's whoami is one of the first's.
	sent := f.sink.sentChannels()
	if len(sent["7dfe84911c9f"]) != 2 || len(sent["246b6f85171f"]) != 2 {
		t.Errorf("files sent: %d and %d", len(sent["7dfe84911c9f"]), len(sent["246b6f85171f"]))
	}
}

// One channel whose file is a duplicate holds another run's times: sent
// for the API to move it to its channel's.
func TestTTYDuplicateSent(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "output")
	dup := `{"eventid":"cowrie.log.closed","session":"s1","timestamp":"2026-10-14T09:59:00.000000Z","ttylog":"var/lib/cowrie/tty/` + tty + `","duplicate":true}`
	f.write(t, LogName, dup, sessionClosedEvent("s1"))
	f.start(t, f.config(f.sink))
	waitFor(t, "upload", func() bool { return len(f.sink.sentChannels()["s1"]) > 0 })
	if sent := f.sink.sentChannels(); len(sent["s1"]) != 1 || sent["s1"][0] != "output" {
		t.Errorf("duplicate sent as %v", sent)
	}
}

// A session's recordings wait for its end: more channels may come.
func TestTTYWaitsForSessionEnd(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "output")
	f.write(t, LogName, logClosedEvent("s1", tty))
	f.start(t, f.config(f.sink))
	q, _ := loadTTYQueue(filepath.Join(f.stateDir, "tty-queue.json"))
	waitFor(t, "queued", func() bool { q, _ = loadTTYQueue(q.path); return q.len() == 1 })
	j, _, due := q.next(time.Now())
	if due || j.Closed || len(j.Channels) != 1 || time.Until(j.NextTry) < sessionWait-time.Minute {
		t.Errorf("job = %+v, due %v", j, due)
	}
	if _, ttys := f.sink.snapshot(); len(ttys) != 0 {
		t.Errorf("uploaded before the session ended: %v", ttys)
	}
}

// Jobs queued by a version that sent one recording per job are sent.
func TestTTYQueueOfOlderVersion(t *testing.T) {
	f := newFixture(t)
	tty := writeTTY(t, f, "output")
	if err := os.MkdirAll(f.stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := `[{"session":"s1","ttylog":"var/lib/cowrie/tty/` + tty + `","attempts":0,"next_try":"2026-10-14T09:00:00Z"}]`
	if err := os.WriteFile(filepath.Join(f.stateDir, "tty-queue.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	f.start(t, f.config(f.sink))
	waitFor(t, "upload", func() bool { _, ttys := f.sink.snapshot(); return ttys["s1"] == "output" })
}

// The attacker chooses how many channels a session opens: the queue keeps
// as many as the API takes in one upload.
func TestTTYChannelsBounded(t *testing.T) {
	q, err := loadTTYQueue(filepath.Join(t.TempDir(), "q.json"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var events []sessionEvent
	for i := range maxChannels + 10 {
		events = append(events, sessionEvent{session: "s1", channel: &ttyChannel{TTYLog: fmt.Sprint(i), ClosedAt: now}})
	}
	if err := q.record(events, now); err != nil {
		t.Fatal(err)
	}
	if j, ok, _ := q.next(now); !ok || len(j.Channels) != maxChannels {
		t.Errorf("channels kept = %d", len(j.Channels))
	}
}
