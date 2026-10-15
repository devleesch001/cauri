package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// Statistics: aggregates over the sessions a StatsFilter selects. Times
// are integer microseconds, so hours and days are integer divisions, the
// same on both engines; they are UTC.

const (
	microsPerHour = int64(time.Hour / time.Microsecond)
	microsPerDay  = 24 * microsPerHour
)

// Summary are the headline numbers of a set of sessions.
type Summary struct {
	Sessions  int `bun:"sessions" json:"sessions"`
	Shells    int `bun:"shells" json:"shells"`         // sessions that obtained a shell
	SourceIPs int `bun:"source_ips" json:"source_ips"` // distinct source addresses
	Countries int `bun:"countries" json:"countries"`   // distinct countries located
	Logins    int `bun:"logins" json:"logins"`         // login attempts
	Accepted  int `bun:"accepted" json:"accepted"`     // successful login attempts
	// Previous is the same for the period of equal length just before,
	// when the filter has both bounds: what the numbers compare to.
	Previous *Summary `bun:"-" json:"previous"`
}

// Summarize returns the headline numbers of the sessions f selects.
func Summarize(ctx context.Context, db bun.IDB, f StatsFilter) (*Summary, error) {
	sum, err := summarize1(ctx, db, f)
	if err != nil || f.From == nil || f.To == nil {
		return sum, err
	}
	span := f.To.Sub(*f.From)
	from, to := f.From.Add(-span), *f.From
	prev := f
	prev.From, prev.To = &from, &to
	sum.Previous, err = summarize1(ctx, db, prev)
	return sum, err
}

func summarize1(ctx context.Context, db bun.IDB, f StatsFilter) (*Summary, error) {
	sum := new(Summary)
	q := db.NewSelect().Model((*Session)(nil)).
		ColumnExpr("COUNT(*) AS sessions").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		ColumnExpr("COUNT(DISTINCT s.src_ip) AS source_ips").
		ColumnExpr("COUNT(DISTINCT s.geo_country) AS countries")
	if err := f.apply(q).Scan(ctx, sum); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*LoginAttempt)(nil)).
		ColumnExpr("COUNT(*) AS logins").
		ColumnExpr("SUM(CASE WHEN la.success THEN 1 ELSE 0 END) AS accepted").
		Join("JOIN sessions AS s ON s.id = la.session_id")
	if err := f.apply(q).Scan(ctx, sum); err != nil {
		return nil, err
	}
	return sum, nil
}

// HourCount is the number of sessions started in an hour of a weekday.
type HourCount struct {
	Weekday  int `bun:"weekday" json:"weekday"` // 0 is Sunday
	Hour     int `bun:"hour" json:"hour"`       // 0 to 23
	Sessions int `bun:"n" json:"sessions"`
}

// DayCount is what happened on a day.
type DayCount struct {
	Day      string `bun:"-" json:"day"` // YYYY-MM-DD
	Index    int64  `bun:"day" json:"-"` // days since 1970-01-01
	Sessions int    `bun:"n" json:"sessions"`
	Shells   int    `bun:"shells" json:"shells"`
}

// Activity is when sessions happen, in UTC.
type Activity struct {
	Hours []HourCount `json:"hours"` // only those with sessions
	Days  []DayCount  `json:"days"`  // in order, only those with sessions
}

// GetActivity returns when the sessions f selects started.
func GetActivity(ctx context.Context, db bun.IDB, f StatsFilter) (*Activity, error) {
	a := &Activity{Hours: []HourCount{}, Days: []DayCount{}}
	// 1970-01-01 was a Thursday, weekday 4.
	q := db.NewSelect().Model((*Session)(nil)).
		ColumnExpr("((s.started_at / ?) + 4) % 7 AS weekday", microsPerDay).
		ColumnExpr("(s.started_at / ?) % 24 AS hour", microsPerHour).
		ColumnExpr("COUNT(*) AS n").
		GroupExpr("1, 2").OrderExpr("1, 2")
	if err := f.apply(q).Scan(ctx, &a.Hours); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*Session)(nil)).
		ColumnExpr("s.started_at / ? AS day", microsPerDay).
		ColumnExpr("COUNT(*) AS n").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		GroupExpr("1").OrderExpr("1")
	if err := f.apply(q).Scan(ctx, &a.Days); err != nil {
		return nil, err
	}
	for i := range a.Days {
		a.Days[i].Day = time.UnixMicro(a.Days[i].Index * microsPerDay).UTC().Format(time.DateOnly)
	}
	return a, nil
}

// ValueCount is a value and how often it was seen.
type ValueCount struct {
	Value string `bun:"value" json:"value"`
	Count int    `bun:"n" json:"count"`
}

// PairCount is a username and password tried together.
type PairCount struct {
	Username string `bun:"username" json:"username"`
	Password string `bun:"password" json:"password"`
	Count    int    `bun:"n" json:"count"`
	Accepted int    `bun:"accepted" json:"accepted"`
}

// LengthCount is the number of attempts with a password of a length;
// Length is MaxPasswordLength for that length and more.
type LengthCount struct {
	Length int `bun:"len" json:"length"`
	Count  int `bun:"n" json:"count"`
}

// Credentials are the logins tried in the sessions a filter selects.
type Credentials struct {
	Attempts  int `bun:"attempts" json:"attempts"`
	Accepted  int `bun:"accepted" json:"accepted"`
	Usernames int `bun:"usernames" json:"distinct_usernames"`
	Passwords int `bun:"passwords" json:"distinct_passwords"`

	TopUsernames []ValueCount  `bun:"-" json:"top_usernames"`
	TopPasswords []ValueCount  `bun:"-" json:"top_passwords"`
	TopPairs     []PairCount   `bun:"-" json:"top_pairs"`
	Worked       []PairCount   `bun:"-" json:"worked"` // pairs that were accepted
	Lengths      []LengthCount `bun:"-" json:"password_lengths"`
}

// Limits of the credential statistics.
const (
	TopCredentials    = 20
	MaxPasswordLength = 16
)

// GetCredentials returns what was tried to log in.
func GetCredentials(ctx context.Context, db bun.IDB, f StatsFilter) (*Credentials, error) {
	attempts := func() *bun.SelectQuery {
		q := db.NewSelect().Model((*LoginAttempt)(nil)).Join("JOIN sessions AS s ON s.id = la.session_id")
		return f.apply(q)
	}
	c := &Credentials{}
	err := attempts().
		ColumnExpr("COUNT(*) AS attempts").
		ColumnExpr("SUM(CASE WHEN la.success THEN 1 ELSE 0 END) AS accepted").
		ColumnExpr("COUNT(DISTINCT la.username) AS usernames").
		ColumnExpr("COUNT(DISTINCT la.password) AS passwords").
		Scan(ctx, c)
	if err != nil {
		return nil, err
	}
	top := func(col string, dst *[]ValueCount) error {
		*dst = []ValueCount{}
		return attempts().ColumnExpr("la."+col+" AS value, COUNT(*) AS n").
			GroupExpr("la."+col).OrderExpr("n DESC, value").Limit(TopCredentials).Scan(ctx, dst)
	}
	if err := top("username", &c.TopUsernames); err != nil {
		return nil, err
	}
	if err := top("password", &c.TopPasswords); err != nil {
		return nil, err
	}
	pairs := func(acceptedOnly bool, dst *[]PairCount) error {
		*dst = []PairCount{}
		q := attempts().ColumnExpr("la.username, la.password, COUNT(*) AS n").
			ColumnExpr("SUM(CASE WHEN la.success THEN 1 ELSE 0 END) AS accepted").
			GroupExpr("la.username, la.password")
		if acceptedOnly {
			q = q.Where("la.success = ?", true).OrderExpr("accepted DESC, la.username, la.password")
		} else {
			q = q.OrderExpr("n DESC, la.username, la.password")
		}
		return q.Limit(TopCredentials).Scan(ctx, dst)
	}
	if err := pairs(false, &c.TopPairs); err != nil {
		return nil, err
	}
	if err := pairs(true, &c.Worked); err != nil {
		return nil, err
	}
	c.Lengths = []LengthCount{}
	err = attempts().
		ColumnExpr("CASE WHEN LENGTH(la.password) >= ? THEN ? ELSE LENGTH(la.password) END AS len", MaxPasswordLength, MaxPasswordLength).
		ColumnExpr("COUNT(*) AS n").GroupExpr("1").OrderExpr("1").Scan(ctx, &c.Lengths)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Payload is a file attackers downloaded to the honeypot or uploaded to
// it, by digest.
type Payload struct {
	SHA256    string  `bun:"sha256" json:"sha256"`
	Transfers int     `bun:"transfers" json:"transfers"`
	Downloads int     `bun:"downloads" json:"downloads"`
	Uploads   int     `bun:"uploads" json:"uploads"`
	Sessions  int     `bun:"sessions" json:"sessions"`
	Sources   int     `bun:"sources" json:"sources"` // distinct source addresses
	First     Time    `bun:"first_ts" json:"first"`
	Last      Time    `bun:"last_ts" json:"last"`
	URL       *string `bun:"url" json:"url"`           // one of the URLs it came from
	Filename  *string `bun:"filename" json:"filename"` // one of its names
	// Countries are the countries it came from, most sessions first.
	Countries []string `bun:"-" json:"countries"`
}

// Payloads are the files of the sessions a filter selects.
type Payloads struct {
	Sessions int       `bun:"sessions" json:"sessions"` // sessions that transferred a file
	Files    int       `bun:"files" json:"files"`       // distinct files
	Items    []Payload `bun:"-" json:"items"`
}

// MaxPayloads bounds the files listed, the most widespread first.
const MaxPayloads = 200

// maxPayloadCountries bounds the countries listed per file.
const maxPayloadCountries = 5

// GetPayloads returns the files transferred, by digest.
func GetPayloads(ctx context.Context, db bun.IDB, f StatsFilter) (*Payloads, error) {
	transfers := func() *bun.SelectQuery {
		q := db.NewSelect().Model((*Transfer)(nil)).Join("JOIN sessions AS s ON s.id = x.session_id").
			Where("x.sha256 IS NOT NULL")
		return f.apply(q)
	}
	p := &Payloads{Items: []Payload{}}
	if err := transfers().ColumnExpr("COUNT(DISTINCT x.session_id) AS sessions, COUNT(DISTINCT x.sha256) AS files").
		Scan(ctx, p); err != nil {
		return nil, err
	}
	err := transfers().ColumnExpr("x.sha256, COUNT(*) AS transfers").
		ColumnExpr("SUM(CASE WHEN x.direction = ? THEN 1 ELSE 0 END) AS downloads", Download).
		ColumnExpr("SUM(CASE WHEN x.direction = ? THEN 1 ELSE 0 END) AS uploads", Upload).
		ColumnExpr("COUNT(DISTINCT x.session_id) AS sessions, COUNT(DISTINCT s.src_ip) AS sources").
		ColumnExpr("MIN(x.ts) AS first_ts, MAX(x.ts) AS last_ts, MIN(x.url) AS url, MIN(x.filename) AS filename").
		GroupExpr("x.sha256").OrderExpr("sessions DESC, last_ts DESC, x.sha256").Limit(MaxPayloads).
		Scan(ctx, &p.Items)
	if err != nil || len(p.Items) == 0 {
		return p, err
	}

	shas := make([]string, len(p.Items))
	index := make(map[string]*Payload, len(p.Items))
	for i := range p.Items {
		shas[i] = p.Items[i].SHA256
		p.Items[i].Countries = []string{}
		index[shas[i]] = &p.Items[i]
	}
	var byCountry []struct {
		SHA256  string `bun:"sha256"`
		Country string `bun:"geo_country"`
		N       int    `bun:"n"`
	}
	err = transfers().ColumnExpr("x.sha256, s.geo_country").ColumnExpr("COUNT(DISTINCT x.session_id) AS n").
		Where("x.sha256 IN (?)", bun.List(shas)).Where("s.geo_country IS NOT NULL").
		GroupExpr("x.sha256, s.geo_country").OrderExpr("x.sha256, n DESC, s.geo_country").
		Scan(ctx, &byCountry)
	if err != nil {
		return nil, err
	}
	for _, r := range byCountry {
		if item := index[r.SHA256]; len(item.Countries) < maxPayloadCountries {
			item.Countries = append(item.Countries, r.Country)
		}
	}
	return p, nil
}

// SourceCount is a source address and its sessions.
type SourceCount struct {
	IP       string  `bun:"src_ip" json:"ip"`
	Country  *string `bun:"country" json:"country"`
	Sessions int     `bun:"n" json:"sessions"`
	Shells   int     `bun:"shells" json:"shells"`
	Last     Time    `bun:"last_ts" json:"last"`
}

// ClientCount is an SSH client version and its sessions.
type ClientCount struct {
	Client   string `bun:"client_version" json:"client"`
	Sessions int    `bun:"n" json:"sessions"`
	Sources  int    `bun:"sources" json:"sources"` // distinct source addresses
}

// NetworkCount is an autonomous system and its sessions.
type NetworkCount struct {
	ASN      int64  `bun:"asn" json:"asn"`
	Org      string `bun:"org" json:"org"`
	Sessions int    `bun:"n" json:"sessions"`
	Sources  int    `bun:"sources" json:"sources"` // distinct source addresses
}

// Origins are who attacks: the busiest addresses, their networks (with an
// ASN database) and the clients used.
type Origins struct {
	Sources  []SourceCount  `json:"sources"`
	Networks []NetworkCount `json:"networks"`
	Clients  []ClientCount  `json:"clients"`
}

// TopOrigins bounds the addresses and clients listed.
const TopOrigins = 20

// GetOrigins returns the busiest source addresses and SSH clients.
func GetOrigins(ctx context.Context, db bun.IDB, f StatsFilter) (*Origins, error) {
	o := &Origins{Sources: []SourceCount{}, Networks: []NetworkCount{}, Clients: []ClientCount{}}
	q := db.NewSelect().Model((*Session)(nil)).Column("src_ip").
		ColumnExpr("MIN(s.geo_country) AS country, COUNT(*) AS n, MAX(s.started_at) AS last_ts").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		Where("s.src_ip IS NOT NULL").GroupExpr("s.src_ip").OrderExpr("n DESC, s.src_ip").Limit(TopOrigins)
	if err := f.apply(q).Scan(ctx, &o.Sources); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*Session)(nil)).Column("asn").
		ColumnExpr("MIN(s.as_org) AS org, COUNT(*) AS n, COUNT(DISTINCT s.src_ip) AS sources").
		Where("s.asn IS NOT NULL").GroupExpr("s.asn").OrderExpr("n DESC, s.asn").Limit(TopOrigins)
	if err := f.apply(q).Scan(ctx, &o.Networks); err != nil {
		return nil, err
	}
	q = db.NewSelect().Model((*Session)(nil)).Column("client_version").
		ColumnExpr("COUNT(*) AS n, COUNT(DISTINCT s.src_ip) AS sources").
		Where("s.client_version IS NOT NULL").GroupExpr("s.client_version").
		OrderExpr("n DESC, s.client_version").Limit(TopOrigins)
	if err := f.apply(q).Scan(ctx, &o.Clients); err != nil {
		return nil, err
	}
	return o, nil
}
