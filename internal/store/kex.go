package store

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/uptrace/bun"
)

// An attacker can also be told by its key exchange: the combination of
// algorithm lists its SSH tool offers (ssh_kex), the same whatever address
// it comes from.

// SortSources sorts by distinct source addresses.
const SortSources = "sources"

var kexSort = sortable{
	def: SortSessions,
	cols: map[string]column{
		SortSessions: {"n", false},
		SortSources:  {"sources", false},
		SortLastSeen: {"last_ts", false},
		SortLoggedIn: {"logged_in", false},
	},
	tie: []string{"n DESC", "last_ts DESC", "k.id"},
}

// KexSorts names the columns the key exchange list sorts by.
func KexSorts() []string { return kexSort.names() }

// KexFilter selects key exchanges. The statistics filter applies to their
// sessions; zero values do not filter.
type KexFilter struct {
	StatsFilter
	HASSH    string // Cowrie's fingerprint, exactly
	LoggedIn *bool  // logged in at least once, or never
	Sort     Sort   // by SortSessions (the default), SortSources, SortLastSeen or SortLoggedIn
	Page
}

// KexAttacker is a key exchange and what came with it.
type KexAttacker struct {
	ID    int64   `bun:"id" json:"id"`
	HASSH *string `bun:"hassh" json:"hassh"`
	// Its lists, as in SSHKex; the host key algorithms tell apart the
	// combinations of one HASSH.
	KexAlgs   *string `bun:"kex_algs" json:"kex_algs"`
	EncAlgs   *string `bun:"enc_algs" json:"enc_algs"`
	MacAlgs   *string `bun:"mac_algs" json:"mac_algs"`
	CompAlgs  *string `bun:"comp_algs" json:"comp_algs"`
	KeyAlgs   *string `bun:"key_algs" json:"key_algs"`
	LangAlgs  *string `bun:"lang_algs" json:"lang_algs"`
	Sessions  int     `bun:"n" json:"sessions"`
	Sources   int     `bun:"sources" json:"sources"`     // distinct source addresses
	LoggedIn  int     `bun:"logged_in" json:"logged_in"` // sessions that logged in
	Logins    int     `bun:"-" json:"logins"`            // login attempts
	FirstSeen Time    `bun:"first_ts" json:"first_seen"`
	LastSeen  Time    `bun:"last_ts" json:"last_seen"`
	// Sessions by stored verdict (see package verdict).
	Humans        int `bun:"humans" json:"humans"`
	Bots          int `bun:"bots" json:"bots"`
	Uncertain     int `bun:"uncertain" json:"uncertain"`
	NoInteraction int `bun:"no_interaction" json:"no_interaction"`
}

// selectKex groups the sessions f selects by key exchange. Grouping by its
// primary key lets both engines select its other columns.
func selectKex(db bun.IDB, f StatsFilter) *bun.SelectQuery {
	q := db.NewSelect().Model((*Session)(nil)).
		ColumnExpr("k.id, k.hassh, k.kex_algs, k.enc_algs, k.mac_algs, k.comp_algs, k.key_algs, k.lang_algs").
		ColumnExpr("COUNT(*) AS n, COUNT(DISTINCT s.src_ip) AS sources").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS logged_in").
		ColumnExpr("MIN(s.started_at) AS first_ts, MAX(s.started_at) AS last_ts").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'human' THEN 1 ELSE 0 END) AS humans").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'bot' THEN 1 ELSE 0 END) AS bots").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'uncertain' THEN 1 ELSE 0 END) AS uncertain").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'no_interaction' THEN 1 ELSE 0 END) AS no_interaction").
		Join("JOIN ssh_kex AS k ON k.id = s.ssh_kex_id").GroupExpr("k.id")
	return f.apply(q)
}

// ListKex returns a page of key exchanges and how many f selects.
func ListKex(ctx context.Context, db bun.IDB, f KexFilter) ([]KexAttacker, int, error) {
	rows := []KexAttacker{}
	q := selectKex(db, f.StatsFilter)
	if f.HASSH != "" {
		q = q.Where("k.hassh = ?", f.HASSH)
	}
	if f.LoggedIn != nil {
		if *f.LoggedIn {
			q = q.Having("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) > 0")
		} else {
			q = q.Having("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) = 0")
		}
	}
	total, err := f.Page.apply(kexSort.order(q, f.Sort)).ScanAndCount(ctx, &rows)
	if err != nil {
		return nil, 0, err
	}
	return rows, int(total), countKexLogins(ctx, db, f.StatsFilter, rows)
}

// countKexLogins sets the login attempts of key exchanges, in the sessions
// f selects: counted for the page only, apart from the grouping.
func countKexLogins(ctx context.Context, db bun.IDB, f StatsFilter, rows []KexAttacker) error {
	if len(rows) == 0 {
		return nil
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	var counts []struct {
		ID int64 `bun:"ssh_kex_id"`
		N  int   `bun:"n"`
	}
	q := db.NewSelect().Model((*LoginAttempt)(nil)).ColumnExpr("s.ssh_kex_id, COUNT(*) AS n").
		Join("JOIN sessions AS s ON s.id = la.session_id").
		Where("s.ssh_kex_id IN (?)", bun.List(ids)).GroupExpr("s.ssh_kex_id")
	if err := f.apply(q).Scan(ctx, &counts); err != nil {
		return err
	}
	byID := make(map[int64]int, len(counts))
	for _, c := range counts {
		byID[c.ID] = c.N
	}
	for i := range rows {
		rows[i].Logins = byID[rows[i].ID]
	}
	return nil
}

// KexSource is an address that used a key exchange.
type KexSource struct {
	IP       string  `bun:"src_ip" json:"ip"`
	Country  *string `bun:"country" json:"country"`
	Sessions int     `bun:"n" json:"sessions"`
	LoggedIn int     `bun:"logged_in" json:"logged_in"`
}

// KexVariant is another combination of the same HASSH, with its sessions.
type KexVariant struct {
	ID       int64   `bun:"id" json:"id"`
	KeyAlgs  *string `bun:"key_algs" json:"key_algs"`
	Sessions int     `bun:"n" json:"sessions"`
}

// KexDetail is a key exchange with what it links to.
type KexDetail struct {
	KexAttacker
	Sensors   []string      `json:"sensors"`   // sensors it reached
	Clients   []ClientCount `json:"clients"`   // SSH clients announced with it, most used first
	Addresses []KexSource   `json:"addresses"` // that used it, most sessions first
	Variants  []KexVariant  `json:"variants"`  // the other combinations of its HASSH, in every session
	Keys      []OfferedKey  `json:"public_keys"`
}

// TopKexSources bounds the addresses listed with a key exchange.
const TopKexSources = 200

// GetKex returns a key exchange, from the sessions f selects, or
// ErrNotFound if it has none.
func GetKex(ctx context.Context, db bun.IDB, id int64, f StatsFilter) (*KexDetail, error) {
	f.Kex = id
	var rows []KexAttacker
	if err := selectKex(db, f).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if err := countKexLogins(ctx, db, f, rows); err != nil {
		return nil, err
	}
	d := &KexDetail{
		KexAttacker: rows[0], Sensors: []string{}, Clients: []ClientCount{}, Addresses: []KexSource{}, Variants: []KexVariant{},
	}
	q := db.NewSelect().Model((*Session)(nil)).ColumnExpr("DISTINCT sn.name").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id").OrderExpr("sn.name")
	if err := f.apply(q).Scan(ctx, &d.Sensors); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*Session)(nil)).Column("client_version").
		ColumnExpr("COUNT(*) AS n, COUNT(DISTINCT s.src_ip) AS sources").
		Where("s.client_version IS NOT NULL").GroupExpr("s.client_version").OrderExpr("n DESC, s.client_version")
	if err := f.apply(q).Scan(ctx, &d.Clients); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*Session)(nil)).Column("src_ip").
		ColumnExpr("MAX(s.geo_country) AS country, COUNT(*) AS n").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS logged_in").
		Where("s.src_ip IS NOT NULL").GroupExpr("s.src_ip").OrderExpr("n DESC, s.src_ip").Limit(TopKexSources)
	if err := f.apply(q).Scan(ctx, &d.Addresses); err != nil {
		return nil, err
	}
	if d.HASSH != nil {
		if err := db.NewSelect().Model((*SSHKex)(nil)).ColumnExpr("k.id, k.key_algs, COUNT(s.id) AS n").
			Join("LEFT JOIN sessions AS s ON s.ssh_kex_id = k.id").
			Where("k.hassh = ?", *d.HASSH).Where("k.id <> ?", id).
			GroupExpr("k.id").OrderExpr("n DESC, k.id").Scan(ctx, &d.Variants); err != nil {
			return nil, err
		}
	}
	keys, err := OfferedKeys(ctx, db, f)
	if err != nil {
		return nil, err
	}
	d.Keys = keys
	return d, nil
}

// KexUse is a key exchange of an attacker, with its sessions.
type KexUse struct {
	ID       int64   `bun:"id" json:"id"`
	HASSH    *string `bun:"hassh" json:"hassh"`
	KeyAlgs  *string `bun:"key_algs" json:"key_algs"`
	Sessions int     `bun:"n" json:"sessions"`
}

// kexUses lists the key exchanges of the sessions f selects.
func kexUses(ctx context.Context, db bun.IDB, f StatsFilter) ([]KexUse, error) {
	out := []KexUse{}
	q := db.NewSelect().Model((*Session)(nil)).ColumnExpr("k.id, k.hassh, k.key_algs, COUNT(*) AS n").
		Join("JOIN ssh_kex AS k ON k.id = s.ssh_kex_id").GroupExpr("k.id").OrderExpr("n DESC, k.id")
	err := f.apply(q).Scan(ctx, &out)
	return out, err
}

// OfferedKey is a public key offered to log in (cowrie.client.fingerprint),
// with how many sessions offered it. It is read from the events' data:
// shown, not queried.
type OfferedKey struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`
	Key         string `json:"key"`
	Sessions    int    `json:"sessions"`
}

// maxOfferedKeys bounds the events read for the keys of an attacker.
const maxOfferedKeys = 5000

// OfferedKeys returns the public keys the sessions f selects offered, most
// offered first.
func OfferedKeys(ctx context.Context, db bun.IDB, f StatsFilter) ([]OfferedKey, error) {
	var rows []struct {
		SessionID int64  `bun:"session_id"`
		Data      string `bun:"data"`
	}
	q := db.NewSelect().TableExpr("events AS e").ColumnExpr("e.session_id, e.data").
		Join("JOIN sessions AS s ON s.id = e.session_id").
		Where("e.type = ?", "cowrie.client.fingerprint").Where("e.data IS NOT NULL").
		OrderExpr("e.id").Limit(maxOfferedKeys)
	if err := f.apply(q).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	byFP := map[string]*OfferedKey{}
	seen := map[string]map[int64]bool{}
	out := []OfferedKey{}
	var order []string
	for _, r := range rows {
		var k struct {
			Fingerprint string `json:"fingerprint"`
			Type        string `json:"type"`
			Key         string `json:"key"`
		}
		if json.Unmarshal([]byte(r.Data), &k) != nil || k.Fingerprint == "" {
			continue
		}
		if _, ok := byFP[k.Fingerprint]; !ok {
			byFP[k.Fingerprint] = &OfferedKey{Fingerprint: k.Fingerprint, Type: k.Type, Key: k.Key}
			seen[k.Fingerprint] = map[int64]bool{}
			order = append(order, k.Fingerprint)
		}
		if !seen[k.Fingerprint][r.SessionID] {
			seen[k.Fingerprint][r.SessionID] = true
			byFP[k.Fingerprint].Sessions++
		}
	}
	for _, fp := range order {
		out = append(out, *byFP[fp])
	}
	slices.SortStableFunc(out, func(a, b OfferedKey) int { return b.Sessions - a.Sessions })
	return out, nil
}
