package purge_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/purge"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/protocol"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// seed stores three sessions on hp-01, each with a recording: 1 and 2 from
// 203.0.113.1 share "shared", 3 from 203.0.113.3 has "own".
func seed(t *testing.T, db *bun.DB) *ttystore.Store {
	t.Helper()
	tty, err := ttystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := ingest.New(db, tty, 0)
	ctx := context.Background()
	for i, s := range []struct{ session, ip, tty string }{
		{"1", "203.0.113.1", "shared"}, {"2", "203.0.113.1", "shared"}, {"3", "203.0.113.3", "own"},
	} {
		b := protocol.EventBatch{SensorID: "hp-01"}
		for j, e := range []map[string]any{
			{"eventid": "cowrie.session.connect"},
			{"eventid": "cowrie.login.failed", "username": "root", "password": "root"},
		} {
			e["session"], e["src_ip"] = s.session, s.ip
			e["timestamp"] = t0.Add(time.Duration(i)*time.Hour + time.Duration(j)*time.Second).Format(time.RFC3339Nano)
			raw, _ := json.Marshal(e)
			b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", raw), Raw: raw})
		}
		if _, err := svc.IngestEvents(ctx, "hp-01", b); err != nil {
			t.Fatal(err)
		}
		m := protocol.TTYMeta{SensorID: "hp-01", Session: s.session, SHA256: digest(s.tty), Size: int64(len(s.tty))}
		if _, err := svc.StoreTTY(ctx, "hp-01", m, strings.NewReader(s.tty)); err != nil {
			t.Fatal(err)
		}
	}
	return tty
}

func stored(t *testing.T, tty *ttystore.Store, content string) bool {
	t.Helper()
	f, err := tty.Open(digest(content))
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return true
}

func TestSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		tty := seed(t, db)
		p := purge.New(db, tty)
		ctx := context.Background()

		if _, err := p.Sessions(ctx, store.SessionFilter{Page: store.Page{Limit: 10}}, true); !errors.Is(err, purge.ErrNoCriteria) {
			t.Fatalf("no criteria: %v", err)
		}

		// A dry run counts and deletes nothing.
		first := store.SessionFilter{SrcIP: "203.0.113.1"}
		want := store.PurgeCounts{Sessions: 2, Events: 4, LoginAttempts: 2, Recordings: 1}
		if c, err := p.Sessions(ctx, first, false); err != nil || c != want {
			t.Fatalf("dry run = %+v, %v; want %+v", c, err, want)
		}
		if !stored(t, tty, "shared") {
			t.Fatal("dry run removed a file")
		}

		if c, err := p.Sessions(ctx, first, true); err != nil || c != want {
			t.Fatalf("purge = %+v, %v; want %+v", c, err, want)
		}
		if stored(t, tty, "shared") || !stored(t, tty, "own") {
			t.Fatal("wrong files removed")
		}
		if c, err := p.Sessions(ctx, first, true); err != nil || c != (store.PurgeCounts{}) {
			t.Fatalf("purge again = %+v, %v", c, err)
		}
		if _, n, err := store.ListSessions(ctx, db, store.SessionFilter{}); err != nil || n != 1 {
			t.Fatalf("sessions left = %d, %v", n, err)
		}

		// A file already gone from the volume is not an error.
		if err := tty.Remove(digest("own")); err != nil {
			t.Fatal(err)
		}
		if c, err := p.Sessions(ctx, store.SessionFilter{Username: "root"}, true); err != nil || c.Sessions != 1 || c.Recordings != 1 {
			t.Fatalf("purge by username = %+v, %v", c, err)
		}
	})
}
