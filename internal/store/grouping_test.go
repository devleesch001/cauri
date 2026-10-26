package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/pkg/protocol"
)

// seedGrouping adds sessions from 198.51.100.0/24, one per address but two
// from .1, by tool (client and HASSH):
//
//	.1 .2 .40  Go/aaa   the first /26
//	.3         OpenSSH/bbb
//	.70        Go/aaa   the second /26
//	.41        Go/aaa   another network
func seedGrouping(t *testing.T, db *bun.DB) {
	t.Helper()
	svc := seed(t, db)
	ctx := context.Background()
	b := protocol.EventBatch{SensorID: "hp-01"}
	n := 0
	add := func(ip, client, hassh string) {
		n++
		sess := fmt.Sprintf("g%02d", n)
		for i, e := range []ev{
			{"eventid": "cowrie.session.connect"},
			{"eventid": "cowrie.client.version", "version": client},
			{"eventid": "cowrie.client.kex", "hassh": hassh},
			{"eventid": "cowrie.login.failed", "username": "root", "password": sess},
		} {
			e["session"] = sess
			e["src_ip"] = ip
			e["timestamp"] = t0.Add(time.Duration(n)*time.Minute + time.Duration(i)*time.Second).Format(time.RFC3339Nano)
			raw, _ := json.Marshal(e)
			b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", raw), Raw: raw})
		}
	}
	add("198.51.100.1", "SSH-2.0-Go", "aaa")
	add("198.51.100.1", "SSH-2.0-Go", "aaa")
	add("198.51.100.2", "SSH-2.0-Go", "aaa")
	add("198.51.100.40", "SSH-2.0-Go", "aaa")
	add("198.51.100.3", "SSH-2.0-OpenSSH_9.6", "bbb")
	add("198.51.100.70", "SSH-2.0-Go", "aaa")
	add("198.51.100.41", "SSH-2.0-Go", "aaa")
	if _, err := svc.IngestEvents(ctx, "hp-01", b); err != nil {
		t.Fatal(err)
	}
	for asn, ips := range map[int64][]string{64500: {"198.51.100.1", "198.51.100.2", "198.51.100.40", "198.51.100.3", "198.51.100.70"}, 64501: {"198.51.100.41"}} {
		id, err := store.NetworkID(ctx, db, asn, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).Set("network_id = ?", id).
			Where("src_ip IN (?)", bun.List(ips)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// groups describes the groups as "block:ip,ip".
func groups(t *testing.T, db *bun.DB, g store.Grouping) ([]store.AttackerGroup, []string) {
	t.Helper()
	f := store.AttackerFilter{StatsFilter: store.StatsFilter{SrcIP: "198.51.100."}}
	rows, total, err := store.GroupAttackers(context.Background(), db, f, g)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range rows {
		var ips []string
		for _, m := range r.Members {
			ips = append(ips, strings.TrimPrefix(m.IP, "198.51.100."))
		}
		out = append(out, r.Block+":"+strings.Join(ips, ","))
	}
	if total != len(rows) {
		t.Errorf("total %d for %d groups", total, len(rows))
	}
	return rows, out
}

func TestGroupAttackers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		seedGrouping(t, db)
		for name, c := range map[string]struct {
			g    store.Grouping
			want []string
		}{
			// Most sessions first; a single address is a group named by it.
			"default": {store.DefaultGrouping, []string{
				"198.51.100.0/26:1,2,40", "198.51.100.41:41", "198.51.100.70:70", "198.51.100.3:3",
			}},
			"/24": {store.Grouping{Prefix4: 24, Prefix6: 64, ASN: true, Client: true, Kex: true}, []string{
				"198.51.100.0/24:1,2,40,70", "198.51.100.41:41", "198.51.100.3:3",
			}},
			"/24, whatever the network": {store.Grouping{Prefix4: 24, Prefix6: 64, Client: true, Kex: true}, []string{
				"198.51.100.0/24:1,2,40,41,70", "198.51.100.3:3",
			}},
			"/24 alone": {store.Grouping{Prefix4: 24, Prefix6: 64}, []string{
				"198.51.100.0/24:1,2,3,40,41,70",
			}},
			"/32": {store.Grouping{Prefix4: 32, Prefix6: 128, ASN: true}, []string{
				"198.51.100.1:1", "198.51.100.41:41", "198.51.100.70:70", "198.51.100.3:3", "198.51.100.40:40", "198.51.100.2:2",
			}},
		} {
			if _, got := groups(t, db, c.g); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}

		rows, _ := groups(t, db, store.DefaultGrouping)
		g := rows[0]
		if g.IPs != 3 || g.Sessions != 4 || g.Logins != 4 || *g.Client != "SSH-2.0-Go" || *g.HASSH != "aaa" || *g.ASN != 64500 ||
			!g.FirstSeen.Equal(t0.Add(time.Minute)) || !g.LastSeen.Equal(t0.Add(4*time.Minute)) {
			t.Errorf("group = %+v", g)
		}
		page, total, err := store.GroupAttackers(context.Background(), db, store.AttackerFilter{
			StatsFilter: store.StatsFilter{SrcIP: "198.51.100."}, Page: store.Page{Limit: 2, Offset: 2},
		}, store.DefaultGrouping)
		if err != nil || total != 4 || len(page) != 2 || page[0].Block != "198.51.100.70" {
			t.Errorf("page = %+v, total %d, %v", page, total, err)
		}
	})
}
