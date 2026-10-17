package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// execFiles returns the recordings Cowrie v3.1.2 wrote for the exec sample,
// by the end of their name.
func execFiles(t *testing.T) map[string][]byte {
	t.Helper()
	dir := "../ingest/testdata/cowrie-v3.1.2-exec-tty"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()[len(e.Name())-4:]] = data
	}
	return out
}

func ttyFiles(contents ...[]byte) []protocol.TTYFile {
	var out []protocol.TTYFile
	for _, c := range contents {
		sum := sha256.Sum256(c)
		out = append(out, protocol.TTYFile{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c)), Body: bytes.NewReader(c)})
	}
	return out
}

type chaptersBody struct {
	Simulated bool `json:"simulated"`
	Chapters  []struct {
		Time    float64 `json:"time"`
		Command string  `json:"command"`
		Output  string  `json:"output"`
	} `json:"chapters"`
}

// Bots run each command on its own exec channel: the files of a session's
// channels make its one recording, each command with its output, a
// duplicate (Cowrie's file of an earlier run) at its own channel's time,
// in the session or in another one.
func TestTTYChannels(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()
		data, err := os.ReadFile("../ingest/testdata/cowrie-v3.1.2-exec.json")
		if err != nil {
			t.Fatal(err)
		}
		b := protocol.EventBatch{SensorID: "hp-01"}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", []byte(line)), Raw: json.RawMessage(line)})
		}
		if _, err := e.client.SendEvents(ctx, b); err != nil {
			t.Fatal(err)
		}
		f := execFiles(t)
		uname, whoami, cpus := f["8a15"], f["ffc1"], f["db99"]

		// uname -a, whoami, then uname -a again: a duplicate of the first.
		res, err := e.client.SendTTYChannels(ctx, "hp-01", "7dfe84911c9f", ttyFiles(uname, whoami))
		if err != nil || res.Status != protocol.TTYStored {
			t.Fatalf("upload: %+v, %v", res, err)
		}
		var got chaptersBody
		get(t, e, "/api/sessions/"+sid(t, e, "hp-01", "7dfe84911c9f")+"/chapters", http.StatusOK, &got)
		if got.Simulated || len(got.Chapters) != 3 {
			t.Fatalf("chapters = %+v", got)
		}
		for i, want := range []string{"Linux", "root", "Linux"} {
			if c := got.Chapters[i]; !strings.HasPrefix(c.Output, want) {
				t.Errorf("chapter %d %q: output %q", i, c.Command, c.Output)
			}
		}
		if got.Chapters[2].Time < got.Chapters[1].Time {
			t.Errorf("duplicate not at its channel's time: %+v", got.Chapters)
		}

		// The same files again, in another order: the same recording.
		again, err := e.client.SendTTYChannels(ctx, "hp-01", "7dfe84911c9f", ttyFiles(whoami, uname, uname))
		if err != nil || again.Status != protocol.TTYExists || again.SHA256 != res.SHA256 {
			t.Errorf("sent again: %+v, %v", again, err)
		}
		// Fewer files make another recording: the first one stays.
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", "7dfe84911c9f", ttyFiles(uname)); statusOf(err) != http.StatusConflict {
			t.Errorf("other recording: %v", err)
		}

		// whoami is a duplicate of the other session's file.
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", "246b6f85171f", ttyFiles(whoami, cpus)); err != nil {
			t.Fatal(err)
		}
		got = chaptersBody{}
		get(t, e, "/api/sessions/"+sid(t, e, "hp-01", "246b6f85171f")+"/chapters", http.StatusOK, &got)
		if len(got.Chapters) != 2 || !strings.HasPrefix(got.Chapters[0].Output, "root") || got.Chapters[1].Output == "" {
			t.Errorf("chapters of the other session = %+v", got)
		}
		if got.Chapters[0].Time > 1 {
			t.Errorf("duplicate at %v s, not at its channel's start", got.Chapters[0].Time)
		}
	})
}

func TestTTYChannelsErrors(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()
		if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
			t.Fatal(err)
		}
		session := "a1b2c3d4e5f6"
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", "unknown", ttyFiles([]byte(ttyContent))); statusOf(err) != http.StatusNotFound {
			t.Errorf("unknown session: %v", err)
		}
		if _, err := e.client.SendTTYChannels(ctx, "hp-02", session, ttyFiles([]byte(ttyContent))); statusOf(err) != http.StatusForbidden {
			t.Errorf("other sensor: %v", err)
		}
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", session, nil); statusOf(err) != http.StatusBadRequest {
			t.Errorf("no file: %v", err)
		}
		lying := ttyFiles([]byte(ttyContent))
		lying[0].SHA256 = ttySHA()[:63] + "0"
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", session, lying); statusOf(err) != http.StatusUnprocessableEntity {
			t.Errorf("wrong digest: %v", err)
		}
		// The limit (64 KiB here) bounds the files together.
		big := bytes.Repeat([]byte("x"), 40<<10)
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", session, ttyFiles(big, append(big, 'y'))); statusOf(err) != http.StatusRequestEntityTooLarge {
			t.Errorf("too large: %v", err)
		}
		many := make([][]byte, protocol.MaxTTYChannels+1)
		for i := range many {
			many[i] = []byte{byte(i), byte(i >> 8)}
		}
		if _, err := e.client.SendTTYChannels(ctx, "hp-01", session, ttyFiles(many...)); statusOf(err) != http.StatusBadRequest {
			t.Errorf("too many files: %v", err)
		}
		// A single file of no channel is stored as it is.
		res, err := e.client.SendTTYChannels(ctx, "hp-01", session, ttyFiles([]byte(ttyContent)))
		if err != nil || res.SHA256 != ttySHA() {
			t.Errorf("single file: %+v, %v", res, err)
		}
	})
}
