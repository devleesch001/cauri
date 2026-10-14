package ingestor_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	// Cowrie names a recording after a hash of the terminal output, not of
	// the file: name and digest differ.
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

	d, err := store.GetSession(ctx, db, store.SessionID("hp-01", "a1b2c3d4e5f6"))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) != 5 || len(d.CommandList) != 1 || !d.LoginSuccess || *d.TTYSHA256 != sha || *d.TTYLog != name || *d.Duration != 4 || *d.TTYDuration != 2.5 {
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
	if n, _ := db.NewSelect().Model((*store.RawEvent)(nil)).Count(ctx); n != 5 {
		t.Fatalf("raw events = %d after resend, want 5", n)
	}
}
