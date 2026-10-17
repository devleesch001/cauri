package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
)

// A session reads the same once its raw lines are purged: every field the
// lines held comes back from the tables and the events' data, but Cowrie's
// envelope (message, sensor, uuid), which nothing needs.
func TestSessionAfterRawPurge(t *testing.T) {
	envelope := map[string]bool{"eventid": true, "timestamp": true, "session": true, "sensor": true, "uuid": true, "message": true}
	// Transfers: none in the samples; one field of the wrong type, kept by
	// the event's data.
	transfers := []string{
		`{"eventid":"cowrie.session.file_download","session":"s9","timestamp":"2026-10-14T09:00:01Z","src_ip":"10.0.0.9","url":"http://x/a.sh","destfile":"","shasum":"aa","outfile":"var/lib/cowrie/downloads/aa"}`,
		`{"eventid":"cowrie.session.file_upload","session":"s9","timestamp":"2026-10-14T09:00:02Z","src_ip":"10.0.0.9","filename":"b.bin","shasum":"bb"}`,
		`{"eventid":"cowrie.session.file_download","session":"s9","timestamp":"2026-10-14T09:00:03Z","src_ip":"10.0.0.9","url":"http://x/c.sh","shasum":42}`,
		`{"eventid":"cowrie.command.input","session":"s9","timestamp":"2026-10-14T09:00:04Z","src_ip":"10.0.0.9","input":["not","text"]}`,
		`{"eventid":"cowrie.command.input","session":"s9","timestamp":"2026-10-14T09:00:05Z","src_ip":"10.0.0.9","input":"id"}`,
	}
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		svc := newService(t, db)
		ctx := context.Background()
		var lines [][]byte
		for _, f := range []string{"testdata/cowrie-v3.1.2.json", "testdata/cowrie-v3.1.2-exec.json", "testdata/cowrie-56b2abb-fragments.json"} {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, bytes.Split(bytes.TrimSpace(data), []byte("\n"))...)
		}
		for _, l := range transfers {
			lines = append(lines, []byte(l))
		}
		if res, err := svc.IngestEvents(ctx, sensor, batch(sensor, lines...)); err != nil || len(res.Rejected) != 0 {
			t.Fatalf("result = %+v, %v", res, err)
		}
		var ids []int64
		if err := db.NewSelect().Model((*store.Session)(nil)).Column("id").Order("id").Scan(ctx, &ids); err != nil {
			t.Fatal(err)
		}
		before := map[int64]*store.SessionDetail{}
		for _, id := range ids {
			d, err := store.GetSession(ctx, db, id)
			if err != nil {
				t.Fatal(err)
			}
			before[id] = d
		}
		if _, err := store.PurgeRawEvents(ctx, db, time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, id := range ids {
			after, err := store.GetSession(ctx, db, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(after.Events) != len(before[id].Events) {
				t.Fatalf("session %d: %d events, %d before", id, len(after.Events), len(before[id].Events))
			}
			for i, e := range before[id].Events {
				var raw, got map[string]any
				if err := json.Unmarshal(e.Data, &raw); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(after.Events[i].Data, &got); err != nil {
					t.Fatalf("%s: %s: %v", e.EventID, after.Events[i].Data, err)
				}
				for k, v := range raw {
					if !envelope[k] && !reflect.DeepEqual(got[k], v) {
						t.Errorf("session %d %s: %s = %v, want %v", id, e.EventID, k, got[k], v)
					}
				}
				checked++
			}
		}
		if checked < len(lines) {
			t.Errorf("checked %d events of %d lines", checked, len(lines))
		}
	})
}
