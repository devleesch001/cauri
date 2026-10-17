package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/internal/verdict"
	"github.com/devleesch001/cauri/pkg/protocol"
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
	return New(db, tty, 1<<20)
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func getSession(t *testing.T, db *bun.DB, sensorID, sess string) *store.Session {
	t.Helper()
	s := new(store.Session)
	err := db.NewSelect().Model(s).ColumnExpr("s.*, t.sha256 AS tty_sha256, t.size AS tty_size, k.hassh AS hassh").
		ColumnExpr("g.type AS geo_source_type, g.built AS geo_source_built, nw.asn AS asn, nw.org AS as_org").
		Join("LEFT JOIN tty_files AS t ON t.id = s.tty_file_id").
		Join("LEFT JOIN ssh_kex AS k ON k.id = s.ssh_kex_id").
		Join("LEFT JOIN geo_sources AS g ON g.id = s.geo_source_id").
		Join("LEFT JOIN networks AS nw ON nw.id = s.network_id").
		Where("s.id = ?", sessionID(t, db, sensorID, sess)).Scan(context.Background())
	if err != nil {
		t.Fatalf("session %s/%s: %v", sensorID, sess, err)
	}
	return s
}

func sessionID(t *testing.T, db *bun.DB, sensorID, sess string) int64 {
	t.Helper()
	id, err := store.LookupSession(context.Background(), db, sensorID, sess)
	if err != nil {
		t.Fatalf("session %s/%s: %v", sensorID, sess, err)
	}
	return id
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
		case !s.StartedAt.Equal(t0), !s.EndedAt.Equal(t0.Add(9500 * time.Millisecond)), *s.DurationMS != 9500:
			t.Errorf("times: %v %v %v", s.StartedAt, s.EndedAt, *s.DurationMS)
		case *s.TTYLog != sum("tty"), *s.TTYDurationMS != 8900, s.TTYSHA256 != nil, s.TTYSize != nil:
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
		if n := count(t, db, (*store.Event)(nil)); n != len(lines) {
			t.Errorf("events = %d, want all including unprojected ones", n)
		}
		if n := count(t, db, (*store.EventRawData)(nil)); n != len(lines) {
			t.Errorf("event data = %d, want one per event", n)
		}
		var sn store.Sensor
		if err := db.NewSelect().Model(&sn).Scan(ctx); err != nil || sn.Name != sensor || !sn.LastSeen.Equal(t0.Add(9500*time.Millisecond)) {
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
		if n := count(t, db, (*store.Event)(nil)); n != len(lines) {
			t.Errorf("raw events = %d after resend", n)
		}
	})
}

// The same batch sent concurrently is stored once: on PostgreSQL both
// transactions can miss each other's events when checking, and only the
// insert of events decides which one projects them.
func TestIngestConcurrentDuplicates(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		lines := fullSession("a1b2c3d4e5f6", "tty")
		const senders = 4
		results := make(chan protocol.EventBatchResult, senders)
		errs := make(chan error, senders)
		for range senders {
			go func() {
				res, err := svc.IngestEvents(context.Background(), sensor, batch(sensor, lines...))
				results <- res
				errs <- err
			}()
		}
		accepted := 0
		for range senders {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
			accepted += (<-results).Accepted
		}
		if accepted != len(lines) {
			t.Errorf("accepted %d events in all, want %d", accepted, len(lines))
		}
		for model, want := range map[any]int{
			(*store.Event)(nil): len(lines), (*store.EventRawData)(nil): len(lines),
			(*store.LoginAttempt)(nil): 2, (*store.Command)(nil): 1, (*store.Session)(nil): 1,
		} {
			if n := count(t, db, model); n != want {
				t.Errorf("%T: %d rows, want %d", model, n, want)
			}
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
		if !s.StartedAt.Equal(t0) || s.SrcPort == nil || s.EndedAt == nil || !s.LoginSuccess || s.TTYLog == nil {
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

		// As with real Cowrie, the name of the recording is not the digest
		// of the file.
		lines := fullSession("a1b2c3d4e5f6", "terminal output only")
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil {
			t.Fatal(err)
		}

		res, err := svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", content), strings.NewReader(content))
		if err != nil || res.Status != protocol.TTYStored {
			t.Fatalf("store = %+v, %v", res, err)
		}
		if s := getSession(t, db, sensor, "a1b2c3d4e5f6"); *s.TTYSHA256 != sum(content) || *s.TTYSize != int64(len(content)) {
			t.Errorf("session tty = %v %v", *s.TTYSHA256, *s.TTYSize)
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

// Whether a session typed in a shell is read from its recording, with the
// verdict, and kept when the file is gone.
func TestStoreTTYShell(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		files := map[string]string{
			"a1b2c3d4e5f6": "../ttylog/testdata/cowrie-v3.1.2.tty",
			"b1b2c3d4e5f6": "testdata/cowrie-v3.1.2-exec-tty/28ba533b0f3c4df63d6b4a5ead73860697bdf735bb353e4ca928474889eb8a15",
			"c1b2c3d4e5f6": "",
		}
		for sess, file := range files {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, fullSession(sess, "terminal output only")...)); err != nil {
				t.Fatal(err)
			}
			if file == "" {
				continue
			}
			content, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.StoreTTY(ctx, sensor, ttyMeta(sess, string(content)), bytes.NewReader(content)); err != nil {
				t.Fatal(err)
			}
		}
		shell := func(sess string) string {
			if s := getSession(t, db, sensor, sess).Shell; s != nil {
				return fmt.Sprint(*s)
			}
			return "unknown"
		}
		for sess, want := range map[string]string{"a1b2c3d4e5f6": "true", "b1b2c3d4e5f6": "false", "c1b2c3d4e5f6": "unknown"} {
			if got := shell(sess); got != want {
				t.Errorf("%s: shell = %s, want %s", sess, got, want)
			}
		}

		// The file removed to limit retention, then the session judged again.
		s := getSession(t, db, sensor, "a1b2c3d4e5f6")
		if err := svc.ForgetTTY(ctx, *s.TTYSHA256); err != nil {
			t.Fatal(err)
		}
		if err := svc.storeVerdict(ctx, db, s.ID); err != nil {
			t.Fatal(err)
		}
		if got := shell("a1b2c3d4e5f6"); got != "true" {
			t.Errorf("after the file is gone: shell = %s", got)
		}
	})
}

// What the tables do not hold of a line is kept with its event, and a key
// exchange is stored once per combination, whatever the batches.
func TestIngestDataAndKex(t *testing.T) {
	raw, err := os.ReadFile("testdata/cowrie-56b2abb-fragments.json")
	if err != nil {
		t.Fatal(err)
	}
	var lines [][]byte
	var kex []byte
	for _, l := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
		lines = append(lines, l)
		if bytes.Contains(l, []byte(`"cowrie.client.kex"`)) {
			kex = l
		}
	}
	// Another session of the same client.
	other := bytes.Replace(kex, []byte("16bb822161d2"), []byte("26bb822161d2"), 1)
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		for range 2 { // sent again: nothing changes
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil {
				t.Fatal(err)
			}
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, other)); err != nil {
				t.Fatal(err)
			}
		}
		var kexes []store.SSHKex
		if err := db.NewSelect().Model(&kexes).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(kexes) != 1 || *kexes[0].HASSH != "eeca2460550b9ded084ecf2f70a75356" || kexes[0].KeyAlgs == nil ||
			!strings.HasPrefix(*kexes[0].KexAlgs, "mlkem768x25519-sha256,") || *kexes[0].CompAlgs != "none,zlib@openssh.com" ||
			len(kexes[0].Digest) != 32 {
			t.Fatalf("ssh_kex = %+v", kexes)
		}
		for _, sess := range []string{"16bb822161d2", "26bb822161d2"} {
			if s := getSession(t, db, sensor, sess); s.SSHKexID == nil || *s.SSHKexID != kexes[0].ID || *s.HASSH != *kexes[0].HASSH {
				t.Errorf("%s: key exchange %v", sess, s.SSHKexID)
			}
		}
		var data []struct {
			Type string `bun:"type"`
			Data string `bun:"data"`
		}
		if err := db.NewSelect().Model((*store.Event)(nil)).Column("type", "data").Where("data IS NOT NULL").
			OrderExpr("type").Scan(ctx, &data); err != nil {
			t.Fatal(err)
		}
		want := []string{"cowrie.client.fingerprint", "cowrie.client.var", "cowrie.command.chpasswd", "cowrie.command.failed",
			"cowrie.log.closed", "cowrie.login.failed", "cowrie.session.params"}
		if len(data) != len(want) {
			t.Fatalf("events with data = %+v", data)
		}
		for i, d := range data {
			if d.Type != want[i] {
				t.Errorf("event %d with data: %s, want %s", i, d.Type, want[i])
			}
		}
		if data[1].Data != `{"name":"LANG","value":"fr_FR.UTF-8"}` {
			t.Errorf("client.var data = %s", data[1].Data)
		}
	})
}

// Without raw lines, events and what the tables do not hold of them are
// stored all the same.
func TestIngestWithoutRawData(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		svc.SetRawData(false)
		ctx := context.Background()
		l := line("cowrie.client.var", "a1b2c3d4e5f6", 0, map[string]any{"name": "LANG", "value": "C"})
		if res, err := svc.IngestEvents(ctx, sensor, batch(sensor, l)); err != nil || res.Accepted != 1 {
			t.Fatalf("ingest = %+v, %v", res, err)
		}
		if n, err := db.NewSelect().Model((*store.EventRawData)(nil)).Count(ctx); err != nil || n != 0 {
			t.Errorf("raw lines = %d, %v", n, err)
		}
		var data string
		if err := db.NewSelect().Model((*store.Event)(nil)).Column("data").Scan(ctx, &data); err != nil || data != `{"name":"LANG","value":"C"}` {
			t.Errorf("event data = %q, %v", data, err)
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
		big := ttyMeta("s1", "x")
		big.Size = svc.MaxTTYSize() + 1
		if _, err := svc.StoreTTY(ctx, sensor, big, strings.NewReader("x")); !errors.Is(err, ErrTooLarge) {
			t.Errorf("too large: %v", err)
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

// testdata/cowrie-v3.1.2.json was written by the official cowrie/cowrie
// image (v3.1.2) during two failed logins and two shells.
func TestIngestRealCowrieLog(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		data, err := os.ReadFile("testdata/cowrie-v3.1.2.json")
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(bytes.TrimSpace(data), []byte("\n"))
		res, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...))
		if err != nil || res.Accepted != len(lines) || len(res.Rejected) != 0 {
			t.Fatalf("result = %+v, %v", res, err)
		}
		if n := count(t, db, (*store.Session)(nil)); n != 4 {
			t.Errorf("sessions = %d", n)
		}
		s := getSession(t, db, sensor, "6f07bbfeafaa")
		switch {
		case !s.LoginSuccess, *s.ClientVersion != "SSH-2.0-Go", *s.HASSH != "16443846184eafde36765c9bab2f4397":
			t.Errorf("session = %+v", s)
		case s.DurationMS == nil || *s.DurationMS != 991, s.TTYDurationMS == nil || *s.TTYDurationMS != 910:
			t.Errorf("durations from duration_ms: %v %v", s.DurationMS, s.TTYDurationMS)
		case *s.TTYLog != "5d07941f09498a4f65d7453a8d25cabda2a265b3da7eb2b80a7a4fa923c5e12a", s.TTYSHA256 != nil:
			t.Errorf("tty = %v %v", *s.TTYLog, s.TTYSHA256)
		}
		if n := count(t, db, (*store.Command)(nil)); n != 5 {
			t.Errorf("commands = %d", n)
		}
		failed, err := db.NewSelect().Model((*store.LoginAttempt)(nil)).Where("success = ?", false).Count(ctx)
		if err != nil || failed != 2 {
			t.Errorf("failed logins = %d, %v", failed, err)
		}
	})
}

func TestDurations(t *testing.T) {
	for raw, want := range map[string]int64{
		`{"eventid":"x","timestamp":"2026-10-14T09:00:00Z","duration_ms":1500}`:  1500,
		`{"eventid":"x","timestamp":"2026-10-14T09:00:00Z","duration":"2.5"}`:    2500,
		`{"eventid":"x","timestamp":"2026-10-14T09:00:00Z","duration":3}`:        3000,
		`{"eventid":"x","timestamp":"2026-10-14T09:00:00Z","duration_ms":"250"}`: 250,
		`{"eventid":"x","timestamp":"2026-10-14T09:00:00Z","duration":0.0004}`:   0,
	} {
		ev, err := parseEvent([]byte(raw))
		if err != nil || ev.durationMS == nil || *ev.durationMS != want {
			t.Errorf("%s: %v, %v", raw, ev, err)
		}
	}
}

// Unexpected field types must not cost the event.
func TestParseLenient(t *testing.T) {
	ev, err := parseEvent([]byte(`{"eventid":"cowrie.session.connect","timestamp":"2026-10-14T09:00:00Z",` +
		`"session":"s1","src_port":"4242","duration":"soon","username":["a"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.SrcPort != nil || ev.durationMS != nil || ev.Username != nil || ev.Session != "s1" {
		t.Errorf("event = %+v", ev)
	}
	if _, err := parseEvent([]byte(`{"eventid":42,"timestamp":"2026-10-14T09:00:00Z"}`)); err == nil {
		t.Error("want error when eventid is not a string")
	}
}

// fakeLocator locates 203.0.113.0/24 in Iran; its source can change, as
// an updated database would.
type fakeLocator struct {
	source string
	city   string // Tehran if empty
}

func (f *fakeLocator) Locate(ip string) (geoip.Location, geoip.Info, bool) {
	typ, date, _ := strings.Cut(f.source, " ")
	built, _ := time.Parse(time.DateOnly, date)
	src := geoip.Info{Type: typ, Built: built}
	if strings.HasPrefix(ip, "203.0.113.") {
		city := f.city
		if city == "" {
			city = "Tehran"
		}
		return geoip.Location{Country: "IR", City: city, Lat: 35.69, Lon: 51.42, HasCoords: true}, src, true
	}
	return geoip.Location{}, src, false
}

// geoSource is the database that located s, as geoip.Info.Source names it.
func geoSource(s *store.Session) string {
	if s.GeoSourceType == nil {
		return ""
	}
	return *s.GeoSourceType + " " + s.GeoSourceBuilt.UTC().Format(time.DateOnly)
}

func TestIngestLocates(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		loc := &fakeLocator{source: "DBIP-City-Lite 2026-10-01"}
		svc.SetLocator(loc)
		ctx := context.Background()

		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, "s1", 0, nil))); err != nil {
			t.Fatal(err)
		}
		s := getSession(t, db, sensor, "s1")
		if s.GeoCountry == nil || *s.GeoCountry != "IR" || *s.GeoCity != "Tehran" || geoSource(s) != "DBIP-City-Lite 2026-10-01" ||
			s.GeoLat == nil || *s.GeoLat != 35.69 || *s.GeoLon != 51.42 {
			t.Fatalf("located session = %v %v %v %v", s.GeoCountry, s.GeoCity, geoSource(s), s.GeoLat)
		}

		// A later batch, with an updated database, keeps the first location.
		loc.source = "DBIP-City-Lite 2026-11-01"
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evCommandInput, "s1", 1, map[string]any{"input": "id"}))); err != nil {
			t.Fatal(err)
		}
		if s := getSession(t, db, sensor, "s1"); geoSource(s) != "DBIP-City-Lite 2026-10-01" {
			t.Errorf("relocated: %v", geoSource(s))
		}

		// A private address is looked up, but has no country.
		l := line(evConnect, "s2", 0, nil)
		l = bytes.Replace(l, []byte("203.0.113.7"), []byte("10.0.0.9"), 1)
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, l)); err != nil {
			t.Fatal(err)
		}
		if s := getSession(t, db, sensor, "s2"); geoSource(s) == "" || s.GeoCountry != nil || s.GeoCity != nil {
			t.Errorf("private address = %v %v %v", s.GeoCountry, s.GeoCity, geoSource(s))
		}
	})
}

// Sessions stored before a database was configured are located once.
func TestLocateSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		for _, sess := range []string{"a", "b", "c"} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, sess, 0, nil))); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := svc.LocateSessions(ctx); n != 0 || err != nil {
			t.Fatalf("without locator: %d, %v", n, err)
		}
		svc.SetLocator(&fakeLocator{source: "GeoLite2-City 2026-10-01"})
		n, err := svc.LocateSessions(ctx)
		if err != nil || n != 3 {
			t.Fatalf("located %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "b"); s.GeoCountry == nil || *s.GeoCountry != "IR" {
			t.Errorf("session b = %v", s.GeoCountry)
		}
		if n, err := svc.LocateSessions(ctx); n != 0 || err != nil {
			t.Errorf("second run located %d, %v", n, err)
		}
	})
}

// A database configured but not loaded yet (geoip.Open of a missing file)
// answers with no source: sessions stay unlocated, to be located once it
// is loaded, never marked as looked up by a database without a name.
func TestLocateUnloaded(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		loc := &fakeLocator{}
		svc.SetLocator(loc)
		ctx := context.Background()
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, "a", 0, nil))); err != nil {
			t.Fatal(err)
		}
		if s := getSession(t, db, sensor, "a"); s.GeoSourceID != nil || s.GeoCountry != nil {
			t.Fatalf("located without a database: %v %v", s.GeoSourceID, s.GeoCountry)
		}
		if n, err := svc.LocateSessions(ctx); n != 0 || err != nil {
			t.Fatalf("without a database: %d, %v", n, err)
		}
		if n := count(t, db, (*store.GeoSource)(nil)); n != 0 {
			t.Errorf("geo_sources = %d", n)
		}
		loc.source = "DBIP-City-Lite 2026-10-01"
		if n, err := svc.LocateSessions(ctx); n != 1 || err != nil {
			t.Fatalf("once loaded: %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "a"); geoSource(s) != "DBIP-City-Lite 2026-10-01" {
			t.Errorf("source = %q", geoSource(s))
		}
	})
}

// Sessions are judged as they are stored, and again by JudgeSessions when
// the criteria change.
func TestJudgeSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		for _, sess := range []string{"a", "b", "c"} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, sess, 0, nil))); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor,
			line("cowrie.command.input", "c", 1, map[string]any{"input": "uname -a"}))); err != nil {
			t.Fatal(err)
		}
		s := getSession(t, db, sensor, "a")
		if s.Verdict == nil || *s.Verdict != "no_interaction" || s.VerdictVersion == nil || *s.VerdictVersion != verdict.Version {
			t.Fatalf("session a = %v %v", s.Verdict, s.VerdictVersion)
		}
		// A command without a terminal.
		if s := getSession(t, db, sensor, "c"); *s.Verdict != "uncertain" || *s.VerdictScore != -2 {
			t.Errorf("session c = %v %v", *s.Verdict, *s.VerdictScore)
		}
		if n, err := svc.JudgeSessions(ctx); n != 0 || err != nil {
			t.Fatalf("up to date: %d, %v", n, err)
		}

		// Older criteria, and a session stored before verdicts were.
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("verdict_version = ?", "0").
			Where("id = ?", sessionID(t, db, sensor, "a")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("verdict = NULL").Set("verdict_score = NULL").Set("verdict_version = NULL").
			Where("id = ?", sessionID(t, db, sensor, "c")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := svc.JudgeSessions(ctx); n != 2 || err != nil {
			t.Fatalf("judged %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "c"); s.Verdict == nil || *s.Verdict != "uncertain" || *s.VerdictVersion != verdict.Version {
			t.Errorf("session c = %v", s.Verdict)
		}
		if n, err := svc.JudgeSessions(ctx); n != 0 || err != nil {
			t.Errorf("second run judged %d, %v", n, err)
		}
	})
}

// A recording lost from the volume (deleted, volume not mounted) must not
// block ingestion: the session is judged on its events alone.
func TestJudgeMissingRecording(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		dir := t.TempDir()
		tty, err := ttystore.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		svc := New(db, tty, 1<<20)
		ctx := context.Background()
		const content = "recording"
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, fullSession("a1b2c3d4e5f6", "x")...)); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.StoreTTY(ctx, sensor, ttyMeta("a1b2c3d4e5f6", content), strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(dir, ttystore.RelPath(sum(content)))); err != nil {
			t.Fatal(err)
		}

		late := line("cowrie.command.input", "a1b2c3d4e5f6", 30, map[string]any{"input": "id"})
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, late)); err != nil {
			t.Fatalf("events of a session whose recording is missing: %v", err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("verdict_version = NULL").Where("1 = 1").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := svc.JudgeSessions(ctx); n != 1 || err != nil {
			t.Fatalf("judged %d, %v", n, err)
		}
		if res, err := svc.JudgeSession(ctx, sessionID(t, db, sensor, "a1b2c3d4e5f6")); err != nil || res.Verdict == "" {
			t.Errorf("on read: %+v, %v", res, err)
		}
	})
}

// Sessions located before coordinates were stored get them, only where
// the current database gives the same city.
func TestPlaceSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		for _, sess := range []string{"a", "b", "c"} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, sess, 0, nil))); err != nil {
				t.Fatal(err)
			}
		}
		// As located by an older cauri: a, b in Tehran, c in Mashhad, no
		// coordinates.
		old, err := store.GeoSourceID(ctx, db, "old", time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		for sess, city := range map[string]string{"a": "Tehran", "b": "Tehran", "c": "Mashhad"} {
			if _, err := db.NewUpdate().Model((*store.Session)(nil)).
				Set("geo_country = 'IR'").Set("geo_city = ?", city).Set("geo_source_id = ?", old).
				Where("id = ?", sessionID(t, db, sensor, sess)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := svc.PlaceSessions(ctx); n != 0 || err != nil {
			t.Fatalf("without locator: %d, %v", n, err)
		}
		svc.SetLocator(&fakeLocator{source: "new"})
		if n, err := svc.PlaceSessions(ctx); n != 2 || err != nil {
			t.Fatalf("placed %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "b"); s.GeoLat == nil || *s.GeoLat != 35.69 || geoSource(s) != "old 1970-01-01" {
			t.Errorf("session b = %v %v", s.GeoLat, geoSource(s))
		}
		// The database now says Tehran for c's address: c keeps its city
		// and stays without a point.
		if s := getSession(t, db, sensor, "c"); s.GeoLat != nil || *s.GeoCity != "Mashhad" {
			t.Errorf("session c = %v %v", s.GeoLat, *s.GeoCity)
		}
		if n, err := svc.PlaceSessions(ctx); n != 0 || err != nil {
			t.Errorf("second run placed %d, %v", n, err)
		}
	})
}

type fakeNetworker struct{}

func (fakeNetworker) Network(ip string) (geoip.Network, bool) {
	if strings.HasPrefix(ip, "203.0.113.") {
		return geoip.Network{ASN: 64500, Org: "Example Net"}, true
	}
	return geoip.Network{}, false
}

// The network is recorded at ingestion, independently of the location,
// and found later for sessions stored before.
func TestNetworks(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, "old", 0, nil))); err != nil {
			t.Fatal(err)
		}
		if n, err := svc.NetworkSessions(ctx); n != 0 || err != nil {
			t.Fatalf("without database: %d, %v", n, err)
		}
		svc.SetNetworker(fakeNetworker{})
		if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, line(evConnect, "new", 0, nil))); err != nil {
			t.Fatal(err)
		}
		if s := getSession(t, db, sensor, "new"); s.ASN == nil || *s.ASN != 64500 || *s.ASOrg != "Example Net" || s.GeoCountry != nil {
			t.Errorf("new session = %v %v %v", s.ASN, s.ASOrg, s.GeoCountry)
		}
		if n, err := svc.NetworkSessions(ctx); n != 1 || err != nil {
			t.Fatalf("found %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "old"); s.ASN == nil || *s.ASN != 64500 {
			t.Errorf("old session = %v", s.ASN)
		}
	})
}

// Sessions that opened several channels are judged on that, and again when
// the criteria change; a session of one channel whose recording was removed
// keeps the verdict it was given with it.
func TestJudgeChannels(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		closed := func(sess string, at float64, name string) []byte {
			return line("cowrie.log.closed", sess, at, map[string]any{"ttylog": "var/lib/cowrie/tty/" + name, "duration_ms": 4})
		}
		// Four channels, a command each, as a libssh bot does.
		bot := []([]byte){line(evConnect, "bot", 0, nil)}
		for i, name := range []string{"c1", "c2", "c3", "c4"} {
			bot = append(bot, line("cowrie.command.input", "bot", float64(i+1), map[string]any{"input": "id"}), closed("bot", float64(i+1)+0.1, name))
		}
		human := []([]byte){
			line(evConnect, "human", 0, nil),
			line("cowrie.command.input", "human", 1, map[string]any{"input": "id"}),
			closed("human", 2, "h1"),
		}
		for _, lines := range [][][]byte{bot, human} {
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil {
				t.Fatal(err)
			}
		}
		// The recording names make a terminal: only the three channels beyond
		// the first count, -1 each.
		if s := getSession(t, db, sensor, "bot"); *s.VerdictScore != -3 || *s.Verdict != "bot" {
			t.Fatalf("bot = %v %v", *s.Verdict, *s.VerdictScore)
		}
		if s := getSession(t, db, sensor, "human"); *s.VerdictScore != 0 {
			t.Fatalf("one channel = %v", *s.VerdictScore)
		}

		// Both were judged by the previous criteria, which knew no channels;
		// the human's verdict was made with a recording since removed.
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("verdict = 'human'").Set("verdict_score = 5").Set("verdict_version = '1'").
			Where("id = ?", sessionID(t, db, sensor, "human")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("verdict = 'uncertain'").Set("verdict_score = 0").Set("verdict_version = '1'").
			Where("id = ?", sessionID(t, db, sensor, "bot")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := svc.JudgeSessions(ctx); n != 1 || err != nil {
			t.Fatalf("judged %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "bot"); *s.Verdict != "bot" || *s.VerdictVersion != verdict.Version {
			t.Errorf("bot after = %v", *s.Verdict)
		}
		s := getSession(t, db, sensor, "human")
		if *s.Verdict != "human" || *s.VerdictScore != 5 || *s.VerdictVersion != verdict.Version {
			t.Errorf("human after = %v %v %v", *s.Verdict, *s.VerdictScore, *s.VerdictVersion)
		}
	})
}

// The recording the API kept of a session with several channels is the
// first channel's alone, and that of a duplicate is another session's:
// they are detached, and the sessions replay from their commands. A
// session of one channel of its own keeps its recording.
func TestForgetPartialTTYs(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		closed := func(sess string, at float64, name string) []byte {
			return line("cowrie.log.closed", sess, at, map[string]any{
				"ttylog": "var/lib/cowrie/tty/" + name, "duration_ms": 4, "duplicate": sess == "dup",
			})
		}
		for sess, channels := range map[string]int{"many": 3, "one": 1, "dup": 1} {
			lines := [][]byte{line(evConnect, sess, 0, nil)}
			for i := range channels {
				lines = append(lines, closed(sess, float64(i+1), sess+fmt.Sprint(i)))
			}
			if _, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil {
				t.Fatal(err)
			}
			content := "recording of " + sess
			if _, err := svc.StoreTTY(ctx, sensor, ttyMeta(sess, content), strings.NewReader(content)); err != nil {
				t.Fatal(err)
			}
		}
		n, err := svc.ForgetPartialTTYs(ctx)
		if n != 2 || err != nil {
			t.Fatalf("detached %d, %v", n, err)
		}
		if s := getSession(t, db, sensor, "many"); s.TTYFileID != nil {
			t.Error("recording of the session with several channels kept")
		}
		if s := getSession(t, db, sensor, "dup"); s.TTYFileID != nil {
			t.Error("recording of the duplicate kept")
		}
		if s := getSession(t, db, sensor, "one"); s.TTYFileID == nil {
			t.Error("recording of the session with one channel detached")
		}
		if c, _ := db.NewSelect().Model((*store.TTYFile)(nil)).Count(ctx); c != 1 {
			t.Errorf("%d recordings recorded, want 1", c)
		}
		if n, err := svc.ForgetPartialTTYs(ctx); n != 0 || err != nil {
			t.Errorf("second run detached %d, %v", n, err)
		}
	})
}
