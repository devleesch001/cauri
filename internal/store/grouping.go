package store

import (
	"cmp"
	"context"
	"net/netip"
	"slices"

	"github.com/uptrace/bun"
)

// Smart grouping: an attacker often uses many addresses of one block,
// rented from one network, with one tool. The list of attackers by
// address can group them, each criterion chosen by the viewer: the block
// size, and whether the network, the SSH client and the key exchange must
// match too. It is computed on read, from the attackers by address.

// Grouping is how close addresses are grouped.
type Grouping struct {
	Prefix4 int  // block size of IPv4 addresses, as a prefix length
	Prefix6 int  // of IPv6 addresses
	ASN     bool // in the same network
	Client  bool // with the same SSH client version
	Kex     bool // with the same key exchange
}

// Default grouping: a /26 or a /64, same network, client and key exchange.
var DefaultGrouping = Grouping{Prefix4: 26, Prefix6: 64, ASN: true, Client: true, Kex: true}

// Bounds of the block sizes: wider blocks would group unrelated tenants.
const (
	MinPrefix4 = 16
	MinPrefix6 = 32
)

// AttackerGroup is addresses grouped, and what came from them all. A
// single address is a group of its own.
type AttackerGroup struct {
	Block     string  `json:"block"` // the block, such as 109.160.32.0/26
	IPs       int     `json:"ips"`
	Sessions  int     `json:"sessions"`
	LoggedIn  int     `json:"logged_in"`
	Logins    int     `json:"logins"`
	FirstSeen Time    `json:"first_seen"`
	LastSeen  Time    `json:"last_seen"`
	Country   *string `json:"country"` // of its address with the most sessions
	ASN       *int64  `json:"asn"`
	ASOrg     *string `json:"as_org"`
	Client    *string `json:"client"` // most used, as its key exchange
	HASSH     *string `json:"hassh"`
	// Sessions by stored verdict (see package verdict).
	Humans        int `json:"humans"`
	Bots          int `json:"bots"`
	Uncertain     int `json:"uncertain"`
	NoInteraction int `json:"no_interaction"`
	// Members are its addresses, most sessions first.
	Members []Attacker `json:"members"`
}

// GroupAttackers returns a page of the groups of the attackers f selects,
// sorted as attackers are, and how many groups there are.
func GroupAttackers(ctx context.Context, db bun.IDB, f AttackerFilter, g Grouping) ([]AttackerGroup, int, error) {
	var rows []Attacker
	if err := f.query(db).Scan(ctx, &rows); err != nil {
		return nil, 0, err
	}
	var tools map[string]tool
	if g.Client || g.Kex {
		var err error
		if tools, err = toolsOf(ctx, db, f.StatsFilter); err != nil {
			return nil, 0, err
		}
	}

	type key struct {
		block  string
		asn    int64
		client string
		kex    int64
	}
	byKey := map[key]*AttackerGroup{}
	var groups []*AttackerGroup
	for _, r := range rows {
		k := key{block: blockOf(r.IP, g)}
		if g.ASN && r.ASN != nil {
			k.asn = *r.ASN
		}
		t := tools[r.IP]
		if g.Client && t.client != nil {
			k.client = *t.client
		}
		if g.Kex {
			k.kex = t.kex
		}
		grp := byKey[k]
		if grp == nil {
			grp = &AttackerGroup{Block: k.block}
			byKey[k] = grp
			groups = append(groups, grp)
		}
		grp.Members = append(grp.Members, r)
		if len(grp.Members) == 1 || r.Sessions > grp.Members[0].Sessions {
			grp.Country, grp.ASN, grp.ASOrg, grp.Client, grp.HASSH = r.Country, r.ASN, r.ASOrg, t.client, t.hassh
		}
	}
	for _, grp := range groups {
		slices.SortStableFunc(grp.Members, func(a, b Attacker) int {
			return cmp.Or(cmp.Compare(b.Sessions, a.Sessions), cmp.Compare(a.IP, b.IP))
		})
		for i, m := range grp.Members {
			grp.IPs++
			grp.Sessions += m.Sessions
			grp.LoggedIn += m.LoggedIn
			grp.Humans += m.Humans
			grp.Bots += m.Bots
			grp.Uncertain += m.Uncertain
			grp.NoInteraction += m.NoInteraction
			if i == 0 || m.FirstSeen.Before(grp.FirstSeen.Time) {
				grp.FirstSeen = m.FirstSeen
			}
			if i == 0 || m.LastSeen.After(grp.LastSeen.Time) {
				grp.LastSeen = m.LastSeen
			}
		}
		if grp.IPs == 1 {
			grp.Block = grp.Members[0].IP
		}
	}
	sortGroups(groups, f.Sort)

	total := len(groups)
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	start := min(max(f.Offset, 0), total)
	page := groups[start:min(start+min(limit, MaxLimit), total)]
	out := make([]AttackerGroup, len(page))
	var members []Attacker
	for i, grp := range page {
		out[i] = *grp
		members = append(members, grp.Members...)
	}
	// Login attempts are counted for the page only, as for attackers.
	if err := countLogins(ctx, db, f.StatsFilter, members); err != nil {
		return nil, 0, err
	}
	for i := range out {
		out[i].Members, members = members[:len(out[i].Members)], members[len(out[i].Members):]
		for _, m := range out[i].Members {
			out[i].Logins += m.Logins
		}
		out[i].Members = out[i].Members[:min(len(out[i].Members), MaxGroupMembers)]
	}
	return out, total, nil
}

// MaxGroupMembers bounds the addresses listed per group, the most active
// first; IPs counts them all.
const MaxGroupMembers = 200

// blockOf returns the block of an address, such as 109.160.32.0/26. An
// address that does not parse is a block of its own.
func blockOf(ip string, g Grouping) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	bits := g.Prefix6
	if a.Is4() {
		bits = g.Prefix4
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return ip
	}
	return p.String()
}

// tool is the SSH client and key exchange an address used most.
type tool struct {
	client *string
	kex    int64
	hassh  *string
}

// toolsOf returns, by address, the client version and key exchange of the
// sessions f selects that it used most.
func toolsOf(ctx context.Context, db bun.IDB, f StatsFilter) (map[string]tool, error) {
	var uses []struct {
		IP     string  `bun:"src_ip"`
		Client *string `bun:"client_version"`
		Kex    *int64  `bun:"ssh_kex_id"`
		HASSH  *string `bun:"hassh"`
		N      int     `bun:"n"`
	}
	q := db.NewSelect().Model((*Session)(nil)).
		ColumnExpr("s.src_ip, s.client_version, s.ssh_kex_id, MAX(k.hassh) AS hassh, COUNT(*) AS n").
		Join("LEFT JOIN ssh_kex AS k ON k.id = s.ssh_kex_id").
		Where("s.src_ip IS NOT NULL").GroupExpr("s.src_ip, s.client_version, s.ssh_kex_id")
	if err := f.apply(q).Scan(ctx, &uses); err != nil {
		return nil, err
	}
	out := map[string]tool{}
	most := map[string]int{}
	for _, u := range uses {
		// The pair used most; ties go to the first client in order, so
		// that the same sessions always give the same group.
		if n, seen := most[u.IP]; seen && (u.N < n || u.N == n && cmp.Compare(deref(u.Client), deref(out[u.IP].client)) >= 0) {
			continue
		}
		most[u.IP] = u.N
		t := tool{client: u.Client, hassh: u.HASSH}
		if u.Kex != nil {
			t.kex = *u.Kex
		}
		out[u.IP] = t
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// sortGroups orders groups as attackers are (see attackerSort): ties by
// sessions, then the latest seen, then block, so that pages stay stable.
func sortGroups(groups []*AttackerGroup, s Sort) {
	by := func(g *AttackerGroup) int {
		switch s.By {
		case SortLastSeen:
			return int(g.LastSeen.UnixMicro())
		case SortLoggedIn:
			return g.LoggedIn
		}
		return g.Sessions
	}
	slices.SortStableFunc(groups, func(a, b *AttackerGroup) int {
		c := cmp.Compare(by(b), by(a))
		if s.Asc {
			c = -c
		}
		return cmp.Or(c, cmp.Compare(b.Sessions, a.Sessions), b.LastSeen.Compare(a.LastSeen.Time), cmp.Compare(a.Block, b.Block))
	})
}
