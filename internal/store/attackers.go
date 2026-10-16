package store

import (
	"context"
	"strings"

	"github.com/uptrace/bun"
)

// An attacker is a source address: its sessions, grouped on read. Other
// ways to tell one attacker (its network, its tools) may come later.

// Attacker sorts.
const (
	SortSessions = "sessions" // most sessions first, the default
	SortLastSeen = "last_seen"
	SortShells   = "shells"
)

// AttackerFilter selects attackers. The statistics filter applies to their
// sessions; zero values do not filter.
type AttackerFilter struct {
	StatsFilter
	Country  string // ISO 3166-1 alpha-2 code
	ASN      int64
	IPPrefix string // start of the address
	Shell    *bool  // obtained a shell at least once, or never
	Sort     Sort   // by SortSessions (the default), SortLastSeen or SortShells
	Page
}

// Attacker is a source address and what came from it.
type Attacker struct {
	IP        string  `bun:"src_ip" json:"ip"`
	Sessions  int     `bun:"n" json:"sessions"`
	Shells    int     `bun:"shells" json:"shells"` // sessions that obtained a shell
	Logins    int     `bun:"-" json:"logins"`      // login attempts
	FirstSeen Time    `bun:"first_ts" json:"first_seen"`
	LastSeen  Time    `bun:"last_ts" json:"last_seen"`
	Country   *string `bun:"country" json:"country"` // an address has one location, its last
	City      *string `bun:"city" json:"city"`
	ASN       *int64  `bun:"asn" json:"asn"`
	ASOrg     *string `bun:"as_org" json:"as_org"`
	// Sessions by stored verdict (see package verdict).
	Humans        int `bun:"humans" json:"humans"`
	Bots          int `bun:"bots" json:"bots"`
	Uncertain     int `bun:"uncertain" json:"uncertain"`
	NoInteraction int `bun:"no_interaction" json:"no_interaction"`
}

// selectAttackers groups the sessions f selects by source address.
func selectAttackers(db bun.IDB, f StatsFilter) *bun.SelectQuery {
	q := db.NewSelect().Model((*Session)(nil)).Column("src_ip").
		ColumnExpr("COUNT(*) AS n").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		ColumnExpr("MIN(s.started_at) AS first_ts, MAX(s.started_at) AS last_ts").
		ColumnExpr("MAX(s.geo_country) AS country, MAX(s.geo_city) AS city").
		ColumnExpr("MAX(s.asn) AS asn, MAX(s.as_org) AS as_org").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'human' THEN 1 ELSE 0 END) AS humans").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'bot' THEN 1 ELSE 0 END) AS bots").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'uncertain' THEN 1 ELSE 0 END) AS uncertain").
		ColumnExpr("SUM(CASE WHEN s.verdict = 'no_interaction' THEN 1 ELSE 0 END) AS no_interaction").
		Where("s.src_ip IS NOT NULL").GroupExpr("s.src_ip")
	return f.apply(q)
}

// ListAttackers returns a page of attackers and how many f selects.
func ListAttackers(ctx context.Context, db bun.IDB, f AttackerFilter) ([]Attacker, int, error) {
	rows := []Attacker{}
	q := selectAttackers(db, f.StatsFilter)
	if f.Country != "" {
		q = q.Where("s.geo_country = ?", f.Country)
	}
	if f.ASN != 0 {
		q = q.Where("s.asn = ?", f.ASN)
	}
	if f.IPPrefix != "" {
		q = q.Where(`s.src_ip LIKE ? ESCAPE '\'`, likePrefix(f.IPPrefix))
	}
	if f.Shell != nil {
		if *f.Shell {
			q = q.Having("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) > 0")
		} else {
			q = q.Having("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) = 0")
		}
	}
	total, err := f.Page.apply(attackerSort.order(q, f.Sort)).ScanAndCount(ctx, &rows)
	if err != nil {
		return nil, 0, err
	}
	return rows, int(total), countLogins(ctx, db, f.StatsFilter, rows)
}

// countLogins sets the login attempts of attackers, in the sessions f
// selects: counted for the page only, apart from the grouping.
func countLogins(ctx context.Context, db bun.IDB, f StatsFilter, rows []Attacker) error {
	if len(rows) == 0 {
		return nil
	}
	ips := make([]string, len(rows))
	for i, r := range rows {
		ips[i] = r.IP
	}
	var counts []struct {
		IP string `bun:"src_ip"`
		N  int    `bun:"n"`
	}
	q := db.NewSelect().Model((*LoginAttempt)(nil)).ColumnExpr("s.src_ip, COUNT(*) AS n").
		Join("JOIN sessions AS s ON s.id = la.session_id").
		Where("s.src_ip IN (?)", bun.List(ips)).GroupExpr("s.src_ip")
	if err := f.apply(q).Scan(ctx, &counts); err != nil {
		return err
	}
	byIP := make(map[string]int, len(counts))
	for _, c := range counts {
		byIP[c.IP] = c.N
	}
	for i := range rows {
		rows[i].Logins = byIP[rows[i].IP]
	}
	return nil
}

// AttackerDetail is an attacker with what tells it apart.
type AttackerDetail struct {
	Attacker
	Sensors []string      `json:"sensors"` // sensors it reached
	Clients []ClientCount `json:"clients"` // SSH clients it announced, most used first
	HASSH   []string      `json:"hassh"`   // client fingerprints
}

// GetAttacker returns the attacker of an address, from the sessions f
// selects, or ErrNotFound if it has none.
func GetAttacker(ctx context.Context, db bun.IDB, ip string, f StatsFilter) (*AttackerDetail, error) {
	f.SrcIP = ip
	var rows []Attacker
	if err := selectAttackers(db, f).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	if err := countLogins(ctx, db, f, rows); err != nil {
		return nil, err
	}
	d := &AttackerDetail{Attacker: rows[0], Sensors: []string{}, Clients: []ClientCount{}, HASSH: []string{}}
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
	q = db.NewSelect().Model((*Session)(nil)).ColumnExpr("DISTINCT s.hassh").
		Where("s.hassh IS NOT NULL").OrderExpr("s.hassh")
	if err := f.apply(q).Scan(ctx, &d.HASSH); err != nil {
		return nil, err
	}
	return d, nil
}

// likePrefix matches what starts with s, its LIKE wildcards escaped.
func likePrefix(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s) + "%"
}
