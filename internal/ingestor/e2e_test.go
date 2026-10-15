package ingestor_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/ingestor"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/client"
)

// The real chain: files on disk -> ingestor -> HTTP -> API -> database.
func TestEndToEnd(t *testing.T) {
	ctx := context.Background()
	db := storetest.SQLite(t)
	tty, _ := ttystore.New(t.TempDir())
	tokens := auth.NewTokens(db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(api.New(api.Config{DB: db, Ingest: ingest.New(db, tty, 0), Tokens: tokens, Logger: quiet}))
	defer srv.Close()
	_, token, _ := tokens.Create(ctx, "hp-01", "e2e")
	c, _ := client.New(srv.URL, token, srv.Client())

	root := t.TempDir()
	logDir, ttyDir, stateDir := filepath.Join(root, "log"), filepath.Join(root, "tty"), filepath.Join(root, "state")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.MkdirAll(ttyDir, 0o755)
	// Cowrie names a recording after a hash of its input, not of the
	// file: name and digest differ.
	recording := "\x01\x00\x00\x00ttylog header\x1b[0mroot@svr04:~# wget http://x/y\r\n"
	sum := sha256.Sum256([]byte(recording))
	sha := hex.EncodeToString(sum[:])
	nameSum := sha256.Sum256([]byte("terminal output"))
	name := hex.EncodeToString(nameSum[:])
	_ = os.WriteFile(filepath.Join(ttyDir, name), []byte(recording), 0o644)

	ts := func(s int) string { return fmt.Sprintf("2026-10-14T09:00:%02d.000000Z", s) }
	lines := []string{
		`{"eventid":"cowrie.session.connect","session":"a1b2c3d4e5f6","timestamp":"` + ts(0) + `","src_ip":"203.0.113.5","src_port":4000,"dst_port":22,"protocol":"ssh"}`,
		`{"eventid":"cowrie.login.success","session":"a1b2c3d4e5f6","timestamp":"` + ts(1) + `","src_ip":"203.0.113.5","username":"root","password":"root"}`,
		`{"eventid":"cowrie.command.input","session":"a1b2c3d4e5f6","timestamp":"` + ts(2) + `","src_ip":"203.0.113.5","input":"wget http://x/y"}`,
		`{"eventid":"cowrie.log.closed","session":"a1b2c3d4e5f6","timestamp":"` + ts(3) + `","src_ip":"203.0.113.5","ttylog":"var/lib/cowrie/tty/` + name + `","size":` + fmt.Sprint(len("terminal output")) + `,"shasum":"` + name + `","duplicate":false,"duration_ms":2500}`,
		`{"eventid":"cowrie.session.closed","session":"a1b2c3d4e5f6","timestamp":"` + ts(4) + `","src_ip":"203.0.113.5","duration":"4.0"}`,
	}
	var content string
	for _, l := range lines {
		content += l + "\n"
	}
	_ = os.WriteFile(filepath.Join(logDir, ingestor.LogName), []byte(content), 0o644)

	run := func() {
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			done <- ingestor.Run(rctx, ingestor.Config{
				SensorID: "hp-01", LogDir: logDir, TTYDir: ttyDir, StateDir: stateDir,
				Sink: ingestor.NewHTTPSink(c), FlushInterval: 10 * time.Millisecond,
				PollInterval: 5 * time.Millisecond, FromStart: true,
				MinBackoff: 5 * time.Millisecond, Logger: quiet,
			})
		}()
		deadline := time.Now().Add(5 * time.Second)
		for {
			rows, _, err := store.ListSessions(ctx, db, store.SessionFilter{})
			if err == nil && len(rows) == 1 && rows[0].TTYAvailable {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("session not ingested with its recording: %+v, %v", rows, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	run()

	id, err := store.LookupSession(ctx, db, "hp-01", "a1b2c3d4e5f6")
	if err != nil {
		t.Fatal(err)
	}
	d, err := store.GetSession(ctx, db, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) != 5 || len(d.CommandList) != 1 || !d.LoginSuccess || *d.TTYSHA256 != sha || *d.TTYLog != name || *d.DurationMS != 4000 || *d.TTYDurationMS != 2500 {
		t.Fatalf("session = %+v", d)
	}
	f, err := tty.Open(sha)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(f)
	_ = f.Close()
	if string(got) != recording {
		t.Fatalf("stored recording = %q", got)
	}

	// Lost state: everything is sent again and deduplicated by the API.
	_ = os.RemoveAll(stateDir)
	run()
	if n, _ := db.NewSelect().Model((*store.Event)(nil)).Count(ctx); n != 5 {
		t.Fatalf("events = %d after resend, want 5", n)
	}
}

// Sessions running one command per exec channel, as bots do, send no
// recording (a file per channel, duplicates of other runs): the API replays
// them, simulated, with every command (sample of the official Cowrie
// v3.1.2, see internal/ingest).
func TestEndToEndExecChannels(t *testing.T) {
	ctx := context.Background()
	db := storetest.SQLite(t)
	tty, _ := ttystore.New(t.TempDir())
	tokens := auth.NewTokens(db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(api.New(api.Config{DB: db, Ingest: ingest.New(db, tty, 0), TTY: tty, Tokens: tokens, Logger: quiet}))
	defer srv.Close()
	_, token, _ := tokens.Create(ctx, "hp-01", "e2e")
	c, _ := client.New(srv.URL, token, srv.Client())

	root := t.TempDir()
	logDir, ttyDir, stateDir := filepath.Join(root, "log"), filepath.Join(root, "tty"), filepath.Join(root, "state")
	_ = os.MkdirAll(logDir, 0o755)
	_ = os.MkdirAll(ttyDir, 0o755)
	sample := "../ingest/testdata/cowrie-v3.1.2-exec"
	files, _ := filepath.Glob(sample + "-tty/*")
	for _, p := range files {
		data, _ := os.ReadFile(p)
		_ = os.WriteFile(filepath.Join(ttyDir, filepath.Base(p)), data, 0o644)
	}
	data, err := os.ReadFile(sample + ".json")
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(logDir, ingestor.LogName), data, 0o644)

	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- ingestor.Run(rctx, ingestor.Config{
			SensorID: "hp-01", LogDir: logDir, TTYDir: ttyDir, StateDir: stateDir,
			Sink: ingestor.NewHTTPSink(c), FlushInterval: 10 * time.Millisecond,
			PollInterval: 5 * time.Millisecond, FromStart: true,
			MinBackoff: 5 * time.Millisecond, Logger: quiet,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, _, err := store.ListSessions(ctx, db, store.SessionFilter{})
		if err == nil && len(rows) == 2 && rows[0].Commands+rows[1].Commands == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions not ingested: %+v, %v", rows, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	// Let the TTY queue settle: nothing must have been stored.
	time.Sleep(100 * time.Millisecond)
	if n, _ := db.NewSelect().Model((*store.TTYFile)(nil)).Count(ctx); n != 0 {
		t.Fatalf("%d recordings stored for sessions with several channels", n)
	}

	for sess, want := range map[string][]string{
		"7dfe84911c9f": {"uname -a", "whoami", "uname -a"},
		"246b6f85171f": {"whoami", "cat /proc/cpuinfo | grep name | wc -l"},
	} {
		id, err := store.LookupSession(ctx, db, "hp-01", sess)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := srv.Client().Get(fmt.Sprintf("%s/api/sessions/%d/chapters", srv.URL, id))
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Simulated bool `json:"simulated"`
			Chapters  []struct {
				Time    float64 `json:"time"`
				Command string  `json:"command"`
			} `json:"chapters"`
		}
		err = json.NewDecoder(resp.Body).Decode(&got)
		_ = resp.Body.Close()
		if err != nil || !got.Simulated || len(got.Chapters) != len(want) {
			t.Fatalf("%s: chapters = %+v, %v", sess, got.Chapters, err)
		}
		last := -1.0
		for i, ch := range got.Chapters {
			// In order, a few tenths of a second apart as run.
			if ch.Command != want[i] || ch.Time < last || ch.Time > 5 {
				t.Errorf("%s #%d = %+v", sess, i, ch)
			}
			last = ch.Time
		}
	}
}
