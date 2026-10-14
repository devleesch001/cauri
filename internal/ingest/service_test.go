package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"cauri/internal/store"
	"cauri/internal/store/storetest"
	"cauri/internal/ttystore"
	"cauri/pkg/protocol"
)

const sensor = "hp-01"

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// line builds a cowrie.json line for session sess at t0 + offset seconds.
func line(eventid, sess string, offset float64, fields map[string]any) []byte {
	m := map[string]any{
		"eventid":   eventid,
		"session":   sess,
		"timestamp": t0.Add(time.Duration(offset * float64(time.Second))).Format("2006-01-02T15:04:05.000000Z"),
		"src_ip":    "203.0.113.7",
		"sensor":    "cowrie-host",
	}
	for k, v := range fields {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return b
}

func batch(sensorID string, lines ...[]byte) protocol.EventBatch {
	b := protocol.EventBatch{SensorID: sensorID}
	for _, l := range lines {
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID(sensorID, l), Raw: l})
	}
	return b
}

func newService(t *testing.T, db *bun.DB) *Service {
	t.Helper()
	tty, err := ttystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(db, tty)
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func getSession(t *testing.T, db *bun.DB, sensorID, sess string) *store.Session {
	t.Helper()
	s := new(store.Session)
	if err := db.NewSelect().Model(s).Where("id = ?", store.SessionID(sensorID, sess)).Scan(context.Background()); err != nil {
		t.Fatalf("session %s/%s: %v", sensorID, sess, err)
	}
	return s
}

func count(t *testing.T, db *bun.DB, model any) int {
	t.Helper()
	n, err := db.NewSelect().Model(model).Count(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

func fullSession(sess, tty string) [][]byte {
	return [][]byte{
		line(evConnect, sess, 0, map[string]any{"src_port": 51234, "dst_ip": "10.0.0.2", "dst_port": 2222, "protocol": "ssh"}),
		line(evClientVersion, sess, 0.1, map[string]any{"version": "SSH-2.0-Go"}),
		line(evClientKex, sess, 0.2, map[string]any{"hassh": "abcdef0123456789abcdef0123456789"}),
		line(evLoginFailed, sess, 1, map[string]any{"username": "root", "password": "admin"}),
		line(evLoginSuccess, sess, 2, map[string]any{"username": "root", "password": "123456"}),
		line(evCommandInput, sess, 3, map[string]any{"input": "uname -a"}),
		line(evFileDownload, sess, 4, map[string]any{"url": "http://198.51.100.1/x.sh", "destfile": "/tmp/x.sh", "shasum": sum("payload")}),
		line("cowrie.session.params", sess, 4.5, map[string]any{"arch": "linux-x64-lsb"}),
		line(evLogClosed, sess, 9, map[string]any{"ttylog": "var/lib/cowrie/tty/" + sum(tty), "size": len(tty), "shasum": sum(tty), "duplicate": false, "duration": "8.9"}),
		line(evSessionClosed, sess, 9.5, map[string]any{"duration": "9.5"}),
	}
}

func TestIngestFullSession(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		lines := fullSession("a1b2c3d4e5f6", "tty")

		res, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...))
		if err != nil {
			t.Fatal(err)
		}
		if res.Accepted != len(lines) || res.Duplicates != 0 || len(res.Rejected) != 0 {
			t.Fatalf("result = %+v", res)
		}

		s := getSession(t, db, sensor, "a1b2c3d4e5f6")
		switch {
		case *s.SrcIP != "203.0.113.7", *s.SrcPort != 51234, *s.DstPort != 2222, *s.Protocol != "ssh":
			t.Errorf("connection fields: %+v", s)
		case *s.ClientVersion != "SSH-2.0-Go", *s.HASSH != "abcdef0123456789abcdef0123456789":
			t.Errorf("client fields: %+v", s)
		case !s.LoginSuccess:
			t.Error("login_success = false")
		case !s.StartedAt.Equal(t0), !s.EndedAt.Equal(t0.Add(9500 * time.Millisecond)), *s.Duration != 9.5:
			t.Errorf("times: %v %v %v", s.StartedAt, s.EndedAt, *s.Duration)
		case *s.TTYSHA256 != sum("tty"), *s.TTYSize != 3, *s.TTYDuration != 8.9:
			t.Errorf("tty fields: %+v", s)
		}
		if n := count(t, db, (*store.LoginAttempt)(nil)); n != 2 {
			t.Errorf("login attempts = %d", n)
		}
		if n := count(t, db, (*store.Command)(nil)); n != 1 {
			t.Errorf("commands = %d", n)
		}
		var x store.Transfer
		if err := db.NewSelect().Model(&x).Scan(ctx); err != nil || x.Direction != store.Download || *x.Filename != "/tmp/x.sh" {
			t.Errorf("transfer = %+v, %v", x, err)
		}
		if n := count(t, db, (*store.RawEvent)(nil)); n != len(lines) {
			t.Errorf("raw events = %d, want all including unprojected ones", n)
		}
		var sn store.Sensor
		if err := db.NewSelect().Model(&sn).Scan(ctx); err != nil || sn.ID != sensor || !sn.LastSeen.Equal(t0.Add(9500*time.Millisecond)) {
			t.Errorf("sensor = %+v, %v", sn, err)
		}
	})
}

func TestIngestDuplicates(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		lines := fullSession("a1b2c3d4e5f6", "tty")

		b := batch(sensor, lines...)
		b.Events = append(b.Events, b.Events[0]) // duplicate within the batch
		res, err := svc.IngestEvents(ctx, sensor, b)
		if err != nil || res.Accepted != len(lines) || res.Duplicates != 1 {
			t.Fatalf("first = %+v, %v", res, err)
		}
		res, err = svc.IngestEvents(ctx, sensor, batch(sensor, lines...))
		if err != nil || res.Accepted != 0 || res.Duplicates != len(lines) {
			t.Fatalf("resend = %+v, %v", res, err)
		}
		if n := count(t, db, (*store.LoginAttempt)(nil)); n != 2 {
			t.Errorf("login attempts = %d after resend", n)
		}
		if n := count(t, db, (*store.RawEvent)(nil)); n != len(lines) {
			t.Errorf("raw events = %d after resend", n)
		}
	})
}

func TestIngestRejections(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		good := line(evCommandInput, "s1", 0, map[string]any{"input": "id"})
		b := batch(sensor,
			good,
			[]byte(`not json`),
			[]byte(`{"timestamp":"2026-10-14T09:00:00Z"}`),
			[]byte(`{"eventid":"cowrie.x","timestamp":"yesterday"}`),
			line(evConnect, "../etc", 0, nil),
		)
		b.Events = append(b.Events, protocol.Event{ID: "nope", Raw: good})

		res, err := svc.IngestEvents(context.Background(), sensor, b)
		if err != nil {
			t.Fatal(err)
		}
		if res.Accepted != 1 || len(res.Rejected) != 5 {
			t.Fatalf("result = %+v", res)
		}
	})
}

func TestIngestSensorMismatch(t *testing.T) {
	svc := newService(t, storetest.SQLite(t))
	_, err := svc.IngestEvents(context.Background(), "hp-02", batch(sensor, line(evConnect, "s1", 0, nil)))
	if !errors.Is(err, ErrSensorMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestIngestTooManyEvents(t *testing.T) {
	svc := newService(t, storetest.SQLite(t))
	b := protocol.EventBatch{SensorID: sensor, Events: make([]protocol.Event, protocol.MaxBatchEvents+1)}
	if _, err := svc.IngestEvents(context.Background(), sensor, b); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

// Events of one session split over batches, the later ones sent first,
// must give the same session as a single batch.
func TestIngestOutOfOrderBatches(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		lines := fullSession("a1b2c3d4e5f6", "tty")
		for _, part := range [][][]byte{lines[5:], lines[:5]} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, part...)); err != nil {
				t.Fatal(err)
			}
		}
		s := getSession(t, db, sensor, "a1b2c3d4e5f6")
		if !s.StartedAt.Equal(t0) || s.SrcPort == nil || s.EndedAt == nil || !s.LoginSuccess || s.TTYSHA256 == nil {
			t.Fatalf("merged session = %+v", s)
		}
	})
}

func TestIngestSameSessionTwoSensors(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		l := line(evConnect, "a1b2c3d4e5f6", 0, nil)
		for _, sn := range []string{"hp-01", "hp-02"} {
			res, err := svc.IngestEvents(ctx, sn, batch(sn, l))
			if err != nil || res.Accepted != 1 {
				t.Fatalf("%s: %+v, %v", sn, res, err)
			}
		}
		if n := count(t, db, (*store.Session)(nil)); n != 2 {
			t.Fatalf("sessions = %d, want one per sensor", n)
		}
	})
}

// Attackers send NUL bytes and invalid UTF-8; PostgreSQL TEXT refuses both.
func TestIngestHostileStrings(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		l := []byte(`{"eventid":"cowrie.login.failed","session":"s1","timestamp":"2026-10-14T09:00:00Z",` +
			`"username":"root\u0000x","password":"` + "\xff\xfe" + `"}`)
		res, err := svc.IngestEvents(context.Background(), sensor, batch(sensor, l))
		if err != nil || res.Accepted != 1 {
			t.Fatalf("result = %+v, %v", res, err)
		}
		var la store.LoginAttempt
		if err := db.NewSelect().Model(&la).Scan(context.Background()); err != nil {
			t.Fatal(err)
		}
		if la.Username != "root\uFFFDx" || la.Password != "\uFFFD\uFFFD" {
			t.Errorf("username %q, password %q", la.Username, la.Password)
		}
	})
}

func ttyMeta(sess, content string) protocol.TTYMeta {
	return protocol.TTYMeta{SensorID: sensor, Session: sess, SHA256: sum(content), Size: int64(len(content))}
}

func TestStoreTTY(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		const content = "\x1b[1mroot@svr04:~#\x1b[0m uname -a\r\n"

		_, err := svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", content), strings.NewReader(content))
		if !errors.Is(err, ErrUnknownSession) {
			t.Fatalf("before events: err = %v, want ErrUnknownSession", err)
		}

		lines := fullSession("a1b2c3d4e5f6", content)
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil {
			t.Fatal(err)
		}

		res, err := svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", content), strings.NewReader(content))
		if err != nil || res.Status != protocol.TTYStored {
			t.Fatalf("store = %+v, %v", res, err)
		}
		f, err := svc.tty.Open(sum(content))
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(f)
		_ = f.Close()
		if string(got) != content {
			t.Errorf("stored %q", got)
		}

		// Retry of the same upload: idempotent, body not needed.
		res, err = svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", content), strings.NewReader(""))
		if err != nil || res.Status != protocol.TTYExists {
			t.Fatalf("retry = %+v, %v", res, err)
		}

		// Another recording for the same session.
		_, err = svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", "other"), strings.NewReader("other"))
		if !errors.Is(err, ErrTTYConflict) {
			t.Fatalf("other file: err = %v, want ErrTTYConflict", err)
		}
	})
}

// Cowrie stores identical recordings once (duplicate: true): a second
// session pointing at a known file only gets linked.
func TestStoreTTYSharedFile(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		const content = "same output"
		for _, sess := range []string{"s1", "s2"} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, fullSession(sess, content)...)); err != nil {
				t.Fatal(err)
			}
		}
		want := []protocol.TTYStatus{protocol.TTYStored, protocol.TTYExists}
		for i, sess := range []string{"s1", "s2"} {
			res, err := svc.StoreTTY(ctx, sensor, ttyMeta(sess, content), strings.NewReader(content))
			if err != nil || res.Status != want[i] {
				t.Fatalf("%s: %+v, %v", sess, res, err)
			}
		}
		if n := count(t, db, (*store.TTYFile)(nil)); n != 1 {
			t.Errorf("tty files = %d", n)
		}
	})
}

func TestStoreTTYErrors(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		// A session whose log.closed was not seen: the upload links it.
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, "s1", 0, nil))); err != nil {
			t.Fatal(err)
		}

		if _, err := svc.StoreTTY(ctx, "hp-02", ttyMeta("s1", "x"), strings.NewReader("x")); !errors.Is(err, ErrSensorMismatch) {
			t.Errorf("sensor mismatch: %v", err)
		}
		m := ttyMeta("s1", "x")
		m.SHA256 = "bad"
		if _, err := svc.StoreTTY(ctx, sensor, m, strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid digest: %v", err)
		}
		if _, err := svc.StoreTTY(ctx, sensor, ttyMeta("s1", "x"), strings.NewReader("y")); !errors.Is(err, ErrDigestMismatch) {
			t.Errorf("wrong content: %v", err)
		}
		if n := count(t, db, (*store.TTYFile)(nil)); n != 0 {
			t.Errorf("tty files = %d after failed upload", n)
		}

		res, err := svc.StoreTTY(ctx, sensor, ttyMeta("s1", "x"), strings.NewReader("x"))
		if err != nil || res.Status != protocol.TTYStored {
			t.Fatalf("store = %+v, %v", res, err)
		}
		if s := getSession(t, db, sensor, "s1"); s.TTYSHA256 == nil || *s.TTYSHA256 != sum("x") || *s.TTYSize != 1 {
			t.Errorf("session not linked: %+v", s)
		}
	})
}
