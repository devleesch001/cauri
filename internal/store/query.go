package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("not found")

// Page bounds a list query.
type Page struct {
	Limit  int
	Offset int
}

// Page limits.
const (
	DefaultLimit = 50
	MaxLimit     = 500
)

func (p Page) apply(q *bun.SelectQuery) *bun.SelectQuery {
	limit := p.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	return q.Limit(int64(min(limit, MaxLimit))).Offset(int64(max(p.Offset, 0)))
}

// Login outcome filters.
const (
	LoginSuccess = "success"
	LoginFailed  = "failed"
)

// Verdict filters, on the stored verdict (see package verdict). Bots
// include the sessions without interaction, where nobody typed, and the
// uncertain ones: only a clear human verdict is worth singling out. The
// verdicts themselves are unchanged.
const (
	VerdictHuman = "human"
	VerdictBot   = "bot"
)

// SessionFilter selects sessions. Zero values do not filter.
type SessionFilter struct {
	Sensor string // name
	SrcIP  string
	// Username and Password match sessions with such a login attempt.
	Username string
	Password string
	// Client matches a substring of the client version, ignoring case.
	Client      string
	Country     string     // ISO 3166-1 alpha-2 code of the source address
	City        string     // with Country: names are only unique within a country
	From, To    *time.Time // on started_at, To exclusive
	Login       string     // LoginSuccess or LoginFailed
	Verdict     string     // VerdictHuman or VerdictBot
	HasCommands *bool
	HasTTY      *bool
	File        string // SHA-256 of a file the session transferred
	ASN         int64  // autonomous system of the source address
	Command     string // a command line run in the session, exactly
	HASSH       string // client fingerprint
	Page
}

// Credential is a username/password pair tried in a session.
type Credential struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Success  bool   `json:"success"`
}

// maxCredentials bounds the credentials listed per session in a summary.
const maxCredentials = 20

// SessionSummary is a session as shown in lists.
type SessionSummary struct {
	Session
	LoginAttempts int          `json:"login_attempts"`
	Commands      int          `json:"commands"`
	TTYAvailable  bool         `json:"tty_available"`
	Credentials   []Credential `json:"credentials"`
}

// selectSessions selects sessions with the sensor name and the recording
// file, joined for the API.
func selectSessions(db bun.IDB, dst any) *bun.SelectQuery {
	return db.NewSelect().Model(dst).
		ColumnExpr("s.*").
		ColumnExpr("sn.name AS sensor, t.sha256 AS tty_sha256, t.size AS tty_size").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id").
		Join("LEFT JOIN tty_files AS t ON t.id = s.tty_file_id")
}

// ListSessions returns a page of sessions, latest first, and the number
// of sessions matching the filter.
func ListSessions(ctx context.Context, db bun.IDB, f SessionFilter) ([]SessionSummary, int, error) {
	var rows []Session
	q := selectSessions(db, &rows)
	if f.Sensor != "" {
		q = q.Where("sn.name = ?", f.Sensor)
	}
	if f.SrcIP != "" {
		q = q.Where("s.src_ip = ?", f.SrcIP)
	}
	if f.Country != "" {
		q = q.Where("s.geo_country = ?", f.Country)
	}
	if f.City != "" {
		q = q.Where("s.geo_city = ?", f.City)
	}
	if f.Client != "" {
		q = q.Where(`LOWER(s.client_version) LIKE ? ESCAPE '\'`, likeContains(f.Client))
	}
	if f.From != nil {
		q = q.Where("s.started_at >= ?", Micro(*f.From))
	}
	if f.To != nil {
		q = q.Where("s.started_at < ?", Micro(*f.To))
	}
	switch f.Login {
	case LoginSuccess:
		q = q.Where("s.login_success = ?", true)
	case LoginFailed:
		q = q.Where("s.login_success = ?", false)
	}
	q = whereVerdict(q, f.Verdict)
	if f.Username != "" || f.Password != "" {
		// IN, not a correlated EXISTS: the attempts matching the username
		// are read once, not once per session.
		sub := db.NewSelect().TableExpr("login_attempts AS fla").ColumnExpr("fla.session_id")
		if f.Username != "" {
			sub = sub.Where("fla.username = ?", f.Username)
		}
		if f.Password != "" {
			sub = sub.Where("fla.password = ?", f.Password)
		}
		q = q.Where("s.id IN (?)", sub)
	}
	if f.HasCommands != nil {
		sub := db.NewSelect().TableExpr("commands AS fc").ColumnExpr("1").Where("fc.session_id = s.id")
		q = q.Where(existsExpr(*f.HasCommands), sub)
	}
	if f.Command != "" {
		sub := db.NewSelect().TableExpr("commands AS fcm").ColumnExpr("fcm.session_id").Where("fcm.input = ?", f.Command)
		q = q.Where("s.id IN (?)", sub)
	}
	if f.HASSH != "" {
		q = q.Where("s.hassh = ?", f.HASSH)
	}
	if f.ASN != 0 {
		q = q.Where("s.asn = ?", f.ASN)
	}
	if f.File != "" {
		sub := db.NewSelect().TableExpr("transfers AS fx").ColumnExpr("fx.session_id").Where("fx.sha256 = ?", f.File)
		q = q.Where("s.id IN (?)", sub)
	}
	if f.HasTTY != nil {
		if *f.HasTTY {
			q = q.Where("s.tty_file_id IS NOT NULL")
		} else {
			q = q.Where("s.tty_file_id IS NULL")
		}
	}

	total, err := f.Page.apply(q).OrderExpr("s.started_at DESC, s.id DESC").ScanAndCount(ctx)
	if err != nil {
		return nil, 0, err
	}
	sums, err := summarize(ctx, db, rows)
	return sums, int(total), err
}

func existsExpr(exists bool) string {
	if exists {
		return "EXISTS (?)"
	}
	return "NOT EXISTS (?)"
}

// summarize adds counts, credentials and recording availability to a page
// of sessions, with one grouped query per table.
func summarize(ctx context.Context, db bun.IDB, rows []Session) ([]SessionSummary, error) {
	sums := make([]SessionSummary, len(rows))
	if len(rows) == 0 {
		return sums, nil
	}
	ids := make([]int64, len(rows))
	index := map[int64]*SessionSummary{}
	for i, r := range rows {
		ids[i] = r.ID
		sums[i] = SessionSummary{Session: r, Credentials: []Credential{}, TTYAvailable: r.TTYFileID != nil}
		index[r.ID] = &sums[i]
	}

	var logins []LoginAttempt
	if err := db.NewSelect().Model(&logins).Where("session_id IN (?)", bun.List(ids)).
		Order("ts", "id").Scan(ctx); err != nil {
		return nil, err
	}
	for _, l := range logins {
		s := index[l.SessionID]
		s.LoginAttempts++
		if len(s.Credentials) < maxCredentials {
			s.Credentials = append(s.Credentials, Credential{l.Username, l.Password, l.Success})
		}
	}

	var counts []struct {
		SessionID int64 `bun:"session_id"`
		N         int   `bun:"n"`
	}
	if err := db.NewSelect().Model((*Command)(nil)).Column("session_id").ColumnExpr("COUNT(*) AS n").
		Where("session_id IN (?)", bun.List(ids)).Group("session_id").Scan(ctx, &counts); err != nil {
		return nil, err
	}
	for _, c := range counts {
		index[c.SessionID].Commands = c.N
	}
	return sums, nil
}

// EventView is a Cowrie event as returned by the API. Data is the line
// Cowrie wrote, null once purged.
type EventView struct {
	ID      int64           `bun:"id" json:"id"`
	EventID string          `bun:"column:type" json:"eventid"`
	TS      Time            `bun:"ts" json:"ts"`
	Data    json.RawMessage `bun:"data" json:"data"`
}

// SessionDetail is a session with everything it recorded, each list in
// chronological order.
type SessionDetail struct {
	SessionSummary
	LoginAttemptList []LoginAttempt `json:"login_attempt_list"`
	CommandList      []Command      `json:"command_list"`
	Transfers        []Transfer     `json:"transfers"`
	Events           []EventView    `json:"events"`
}

// GetSession returns a session by its identifier.
func GetSession(ctx context.Context, db bun.IDB, id int64) (*SessionDetail, error) {
	var s Session
	err := selectSessions(db, &s).Where("s.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sums, err := summarize(ctx, db, []Session{s})
	if err != nil {
		return nil, err
	}
	d := &SessionDetail{
		SessionSummary:   sums[0],
		LoginAttemptList: []LoginAttempt{},
		CommandList:      []Command{},
		Transfers:        []Transfer{},
		Events:           []EventView{},
	}
	for _, dst := range []any{&d.LoginAttemptList, &d.CommandList, &d.Transfers} {
		if err := db.NewSelect().Model(dst).Where("session_id = ?", id).Order("ts", "id").Scan(ctx); err != nil {
			return nil, err
		}
	}
	err = db.NewSelect().Model((*Event)(nil)).
		ColumnExpr("e.id, e.type, e.ts, ed.data").
		Join("LEFT JOIN event_data AS ed ON ed.event_id = e.id").
		Where("e.session_id = ?", id).OrderExpr("e.ts, e.id").Scan(ctx, &d.Events)
	return d, err
}

// LoginAttemptFilter selects login attempts. Zero values do not filter.
type LoginAttemptFilter struct {
	Sensor   string // name
	SrcIP    string
	Username string
	Password string
	Country  string     // of the session's source address
	From, To *time.Time // on ts, To exclusive
	Success  *bool
	// Interactive filters on whether the session obtained a shell, that is
	// whether any login of the session succeeded.
	Interactive *bool
	Page
}

// LoginAttemptRow is a login attempt with its session context.
type LoginAttemptRow struct {
	ID        int64   `bun:"id" json:"id"`
	SessionID int64   `bun:"session_id" json:"session_id"`
	TS        Time    `bun:"ts" json:"ts"`
	Username  string  `bun:"username" json:"username"`
	Password  string  `bun:"password" json:"password"`
	Success   bool    `bun:"success" json:"success"`
	Sensor    string  `bun:"sensor" json:"sensor"`
	Session   string  `bun:"cowrie_session" json:"session"`
	SrcIP     *string `bun:"src_ip" json:"src_ip"`
	Client    *string `bun:"client_version" json:"client_version"`
	Country   *string `bun:"geo_country" json:"geo_country"`
}

// ListLoginAttempts returns a page of login attempts, latest first, and
// the number of attempts matching the filter.
func ListLoginAttempts(ctx context.Context, db bun.IDB, f LoginAttemptFilter) ([]LoginAttemptRow, int, error) {
	q := db.NewSelect().Model((*LoginAttempt)(nil)).
		ColumnExpr("la.id, la.session_id, la.ts, la.username, la.password, la.success").
		ColumnExpr("sn.name AS sensor, s.cowrie_session, s.src_ip, s.client_version, s.geo_country").
		Join("JOIN sessions AS s ON s.id = la.session_id").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id")
	if f.Sensor != "" {
		q = q.Where("sn.name = ?", f.Sensor)
	}
	if f.SrcIP != "" {
		q = q.Where("s.src_ip = ?", f.SrcIP)
	}
	if f.Username != "" {
		q = q.Where("la.username = ?", f.Username)
	}
	if f.Country != "" {
		q = q.Where("s.geo_country = ?", f.Country)
	}
	if f.Password != "" {
		q = q.Where("la.password = ?", f.Password)
	}
	if f.From != nil {
		q = q.Where("la.ts >= ?", Micro(*f.From))
	}
	if f.To != nil {
		q = q.Where("la.ts < ?", Micro(*f.To))
	}
	if f.Success != nil {
		q = q.Where("la.success = ?", *f.Success)
	}
	if f.Interactive != nil {
		q = q.Where("s.login_success = ?", *f.Interactive)
	}
	rows := []LoginAttemptRow{}
	total, err := f.Page.apply(q).OrderExpr("la.ts DESC, la.id DESC").ScanAndCount(ctx, &rows)
	return rows, int(total), err
}

// ListSensors returns every sensor that sent events, by name.
func ListSensors(ctx context.Context, db bun.IDB) ([]Sensor, error) {
	sensors := []Sensor{}
	err := db.NewSelect().Model(&sensors).Where("first_seen IS NOT NULL").Order("name").Scan(ctx)
	return sensors, err
}

// likeContains returns a LIKE pattern matching s anywhere, case folded,
// to use with ESCAPE '\'.
func likeContains(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(s)) + "%"
}

// ErrNoRecording means the session exists but its TTY recording has not
// been stored (no shell was opened, or the upload has not arrived yet).
var ErrNoRecording = errors.New("no recording")

// TermSize is a terminal size reported by the client during a session.
type TermSize struct {
	TS         Time
	Cols, Rows int
}

// Recording is what replaying a session needs.
type Recording struct {
	Session string // Cowrie session identifier
	SHA256  string // digest of the stored file, see ttystore
	// Sizes are the terminal sizes reported by the client, in time order.
	Sizes []TermSize
	// Commands are the commands typed, in time order: the replay chapters.
	Commands []Command
}

// GetRecording returns the stored recording of a session.
func GetRecording(ctx context.Context, db bun.IDB, id int64) (*Recording, error) {
	var s Session
	err := selectSessions(db, &s).Where("s.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.TTYSHA256 == nil {
		return nil, ErrNoRecording
	}

	rec := &Recording{Session: s.CowrieSession, SHA256: *s.TTYSHA256}
	if err := db.NewSelect().Model(&rec.Commands).Where("session_id = ?", id).
		Order("ts", "id").Scan(ctx); err != nil {
		return nil, err
	}
	// Terminal sizes are only in the raw lines: purging raw data must keep
	// cowrie.client.size (see docs/db.md).
	var sizes []struct {
		TS   Time   `bun:"ts"`
		Data string `bun:"data"`
	}
	if err := db.NewSelect().Model((*Event)(nil)).ColumnExpr("e.ts, ed.data").
		Join("JOIN event_data AS ed ON ed.event_id = e.id").
		Where("e.session_id = ?", id).Where("e.type = ?", "cowrie.client.size").
		OrderExpr("e.ts, e.id").Scan(ctx, &sizes); err != nil {
		return nil, err
	}
	for _, e := range sizes {
		var size struct {
			Width  json.Number `json:"width"`
			Height json.Number `json:"height"`
		}
		if json.Unmarshal([]byte(e.Data), &size) != nil {
			continue
		}
		cols, err1 := size.Width.Int64()
		rows, err2 := size.Height.Int64()
		if err1 == nil && err2 == nil {
			rec.Sizes = append(rec.Sizes, TermSize{TS: e.TS, Cols: int(cols), Rows: int(rows)})
		}
	}
	return rec, nil
}

// SessionFacts are what judging a session needs (see package verdict).
type SessionFacts struct {
	ClientVersion string
	Commands      int
	// Terminal: the session had a pty, as told by a recording or a
	// terminal size.
	Terminal bool
	// RecordingSHA256 is the stored recording, nil if there is none.
	RecordingSHA256 *string
}

// GetSessionFacts returns the facts of a session.
func GetSessionFacts(ctx context.Context, db bun.IDB, id int64) (*SessionFacts, error) {
	var s Session
	err := selectSessions(db, &s).Where("s.id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	f := &SessionFacts{Terminal: s.TTYLog != nil || s.TTYFileID != nil, RecordingSHA256: s.TTYSHA256}
	if s.ClientVersion != nil {
		f.ClientVersion = *s.ClientVersion
	}
	n, err := db.NewSelect().Model((*Command)(nil)).Where("session_id = ?", id).Count(ctx)
	if err != nil {
		return nil, err
	}
	f.Commands = int(n)
	if !f.Terminal {
		f.Terminal, err = db.NewSelect().Model((*Event)(nil)).
			Where("session_id = ?", id).Where("type = ?", "cowrie.client.size").Exists(ctx)
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}

// CountryCount is a country and what came from it.
type CountryCount struct {
	Country  string `bun:"geo_country" json:"country"`
	Sessions int    `bun:"n" json:"sessions"`
	Shells   int    `bun:"shells" json:"shells"` // sessions that obtained a shell
}

// StatsFilter selects the sessions statistics count: by place, time,
// credentials or files. Zero values do not filter.
type StatsFilter struct {
	Sensor   string     // name
	From, To *time.Time // on started_at, To exclusive
	Verdict  string     // VerdictHuman or VerdictBot, as in SessionFilter
}

// ListCountries returns the countries sessions came from, most sessions
// first. Sessions without a location are not counted.
func ListCountries(ctx context.Context, db bun.IDB, f StatsFilter) ([]CountryCount, error) {
	out := []CountryCount{}
	q := db.NewSelect().Model((*Session)(nil)).Column("geo_country").ColumnExpr("COUNT(*) AS n").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		Where("s.geo_country IS NOT NULL")
	q = f.apply(q).GroupExpr("s.geo_country").OrderExpr("n DESC, s.geo_country")
	err := q.Scan(ctx, &out)
	return out, err
}

func (f StatsFilter) apply(q *bun.SelectQuery) *bun.SelectQuery {
	if f.Sensor != "" {
		q = q.Where("s.sensor_id = (SELECT id FROM sensors WHERE name = ?)", f.Sensor)
	}
	if f.From != nil {
		q = q.Where("s.started_at >= ?", Micro(*f.From))
	}
	if f.To != nil {
		q = q.Where("s.started_at < ?", Micro(*f.To))
	}
	return whereVerdict(q, f.Verdict)
}

// CityCount is a city, where it is, and what came from it.
type CityCount struct {
	Country  string  `bun:"geo_country" json:"country"`
	City     string  `bun:"geo_city" json:"city"`
	Lat      float64 `bun:"lat" json:"lat"`
	Lon      float64 `bun:"lon" json:"lon"`
	Sessions int     `bun:"n" json:"sessions"`
	Shells   int     `bun:"shells" json:"shells"`
}

// MaxCities bounds the cities returned, the most active first: enough for
// a world map, small enough to read.
const MaxCities = 500

// ListCities returns the cities sessions came from, most sessions first,
// for the sessions with coordinates. A city's position is the average of
// its sessions': databases of different dates may differ slightly.
func ListCities(ctx context.Context, db bun.IDB, f StatsFilter) ([]CityCount, error) {
	out := []CityCount{}
	q := db.NewSelect().Model((*Session)(nil)).Column("geo_country", "geo_city").
		ColumnExpr("AVG(s.geo_lat) AS lat, AVG(s.geo_lon) AS lon, COUNT(*) AS n").
		ColumnExpr("SUM(CASE WHEN s.login_success THEN 1 ELSE 0 END) AS shells").
		Where("s.geo_city IS NOT NULL").Where("s.geo_lat IS NOT NULL")
	q = f.apply(q).GroupExpr("s.geo_country, s.geo_city").
		OrderExpr("n DESC, s.geo_country, s.geo_city").Limit(MaxCities)
	err := q.Scan(ctx, &out)
	return out, err
}

// whereVerdict filters on the stored verdict, see VerdictHuman.
func whereVerdict(q *bun.SelectQuery, v string) *bun.SelectQuery {
	switch v {
	case VerdictHuman:
		return q.Where("s.verdict = ?", "human")
	case VerdictBot:
		return q.Where("s.verdict IN (?)", bun.List([]string{"bot", "no_interaction", "uncertain"}))
	}
	return q
}

// LookupSession returns the id of a session from its sensor name and
// Cowrie session id.
func LookupSession(ctx context.Context, db bun.IDB, sensor, cowrieSession string) (int64, error) {
	var id int64
	err := db.NewSelect().Model((*Session)(nil)).Column("s.id").
		Join("JOIN sensors AS sn ON sn.id = s.sensor_id").
		Where("sn.name = ?", sensor).Where("s.cowrie_session = ?", cowrieSession).Scan(ctx, &id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}
