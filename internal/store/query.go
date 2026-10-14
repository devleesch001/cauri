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

// SessionFilter selects sessions. Zero values do not filter.
type SessionFilter struct {
	SensorID string
	SrcIP    string
	// Username and Password match sessions with such a login attempt.
	Username string
	Password string
	// Client matches a substring of the client version, ignoring case.
	Client      string
	From, To    *time.Time // on started_at, To exclusive
	Login       string     // LoginSuccess or LoginFailed
	HasCommands *bool
	HasTTY      *bool
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

// ListSessions returns a page of sessions, latest first, and the number
// of sessions matching the filter.
func ListSessions(ctx context.Context, db bun.IDB, f SessionFilter) ([]SessionSummary, int, error) {
	// Aliases are fixed by the models (s for sessions); subqueries use
	// their own so they can refer to the outer row.
	var rows []Session
	q := db.NewSelect().Model(&rows)
	if f.SensorID != "" {
		q = q.Where("s.sensor_id = ?", f.SensorID)
	}
	if f.SrcIP != "" {
		q = q.Where("s.src_ip = ?", f.SrcIP)
	}
	if f.Client != "" {
		q = q.Where(`LOWER(s.client_version) LIKE ? ESCAPE '\'`, likeContains(f.Client))
	}
	if f.From != nil {
		q = q.Where("s.started_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("s.started_at < ?", f.To.UTC())
	}
	switch f.Login {
	case LoginSuccess:
		q = q.Where("s.login_success = ?", true)
	case LoginFailed:
		q = q.Where("s.login_success = ?", false)
	}
	if f.Username != "" || f.Password != "" {
		sub := db.NewSelect().TableExpr("login_attempts AS fla").ColumnExpr("1").Where("fla.session_id = s.id")
		if f.Username != "" {
			sub = sub.Where("fla.username = ?", f.Username)
		}
		if f.Password != "" {
			sub = sub.Where("fla.password = ?", f.Password)
		}
		q = q.Where("EXISTS (?)", sub)
	}
	if f.HasCommands != nil {
		sub := db.NewSelect().TableExpr("commands AS fc").ColumnExpr("1").Where("fc.session_id = s.id")
		q = q.Where(existsExpr(*f.HasCommands), sub)
	}
	if f.HasTTY != nil {
		sub := db.NewSelect().TableExpr("tty_files AS ft").ColumnExpr("1").Where("ft.sha256 = s.tty_sha256")
		q = q.Where(existsExpr(*f.HasTTY), sub)
	}

	total, err := f.Page.apply(q).OrderExpr("s.started_at DESC, s.id").ScanAndCount(ctx)
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
	ids := make([]string, len(rows))
	var shas []string
	index := map[string]*SessionSummary{}
	for i, r := range rows {
		ids[i] = r.ID
		sums[i] = SessionSummary{Session: r, Credentials: []Credential{}}
		index[r.ID] = &sums[i]
		if r.TTYSHA256 != nil {
			shas = append(shas, *r.TTYSHA256)
		}
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
		SessionID string `bun:"session_id"`
		N         int    `bun:"n"`
	}
	if err := db.NewSelect().Model((*Command)(nil)).Column("session_id").ColumnExpr("COUNT(*) AS n").
		Where("session_id IN (?)", bun.List(ids)).Group("session_id").Scan(ctx, &counts); err != nil {
		return nil, err
	}
	for _, c := range counts {
		index[c.SessionID].Commands = c.N
	}

	if len(shas) > 0 {
		var stored []string
		if err := db.NewSelect().Model((*TTYFile)(nil)).Column("sha256").
			Where("sha256 IN (?)", bun.List(shas)).Scan(ctx, &stored); err != nil {
			return nil, err
		}
		have := map[string]bool{}
		for _, s := range stored {
			have[s] = true
		}
		for i := range sums {
			if sha := sums[i].TTYSHA256; sha != nil && have[*sha] {
				sums[i].TTYAvailable = true
			}
		}
	}
	return sums, nil
}

// EventView is a raw Cowrie event as returned by the API.
type EventView struct {
	ID      string          `json:"id"`
	EventID string          `json:"eventid"`
	TS      time.Time       `json:"ts"`
	Data    json.RawMessage `json:"data"`
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

// GetSession returns a session by its internal identifier.
func GetSession(ctx context.Context, db bun.IDB, id string) (*SessionDetail, error) {
	var s Session
	err := db.NewSelect().Model(&s).Where("id = ?", id).Scan(ctx)
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
	var raw []RawEvent
	if err := db.NewSelect().Model(&raw).Where("session_id = ?", id).Order("ts", "id").Scan(ctx); err != nil {
		return nil, err
	}
	for _, r := range raw {
		d.Events = append(d.Events, EventView{ID: r.ID, EventID: r.EventID, TS: r.TS, Data: json.RawMessage(r.Data)})
	}
	return d, nil
}

// LoginAttemptFilter selects login attempts. Zero values do not filter.
type LoginAttemptFilter struct {
	SensorID string
	SrcIP    string
	Username string
	Password string
	From, To *time.Time // on ts, To exclusive
	Success  *bool
	// Interactive filters on whether the session obtained a shell, that is
	// whether any login of the session succeeded.
	Interactive *bool
	Page
}

// LoginAttemptRow is a login attempt with its session context.
type LoginAttemptRow struct {
	ID        string    `bun:"id" json:"id"`
	SessionID string    `bun:"session_id" json:"session_id"`
	TS        time.Time `bun:"ts" json:"ts"`
	Username  string    `bun:"username" json:"username"`
	Password  string    `bun:"password" json:"password"`
	Success   bool      `bun:"success" json:"success"`
	SensorID  string    `bun:"sensor_id" json:"sensor_id"`
	Session   string    `bun:"cowrie_session" json:"session"`
	SrcIP     *string   `bun:"src_ip" json:"src_ip"`
	Client    *string   `bun:"client_version" json:"client_version"`
}

// ListLoginAttempts returns a page of login attempts, latest first, and
// the number of attempts matching the filter.
func ListLoginAttempts(ctx context.Context, db bun.IDB, f LoginAttemptFilter) ([]LoginAttemptRow, int, error) {
	q := db.NewSelect().Model((*LoginAttempt)(nil)).
		ColumnExpr("la.id, la.session_id, la.ts, la.username, la.password, la.success").
		ColumnExpr("s.sensor_id, s.session AS cowrie_session, s.src_ip, s.client_version").
		Join("JOIN sessions AS s ON s.id = la.session_id")
	if f.SensorID != "" {
		q = q.Where("s.sensor_id = ?", f.SensorID)
	}
	if f.SrcIP != "" {
		q = q.Where("s.src_ip = ?", f.SrcIP)
	}
	if f.Username != "" {
		q = q.Where("la.username = ?", f.Username)
	}
	if f.Password != "" {
		q = q.Where("la.password = ?", f.Password)
	}
	if f.From != nil {
		q = q.Where("la.ts >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("la.ts < ?", f.To.UTC())
	}
	if f.Success != nil {
		q = q.Where("la.success = ?", *f.Success)
	}
	if f.Interactive != nil {
		q = q.Where("s.login_success = ?", *f.Interactive)
	}
	rows := []LoginAttemptRow{}
	total, err := f.Page.apply(q).OrderExpr("la.ts DESC, la.id").ScanAndCount(ctx, &rows)
	return rows, int(total), err
}

// ListSensors returns every sensor that sent events.
func ListSensors(ctx context.Context, db bun.IDB) ([]Sensor, error) {
	sensors := []Sensor{}
	err := db.NewSelect().Model(&sensors).Order("id").Scan(ctx)
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
	TS         time.Time
	Cols, Rows int
}

// Recording is what replaying a session needs.
type Recording struct {
	Session string // Cowrie session identifier
	SHA256  string // digest of the stored file, see ttystore
	// Sizes are the terminal sizes reported by the client, in time order.
	Sizes []TermSize
}

// GetRecording returns the stored recording of a session.
func GetRecording(ctx context.Context, db bun.IDB, id string) (*Recording, error) {
	var s Session
	err := db.NewSelect().Model(&s).Column("session", "tty_sha256").Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if s.TTYSHA256 == nil {
		return nil, ErrNoRecording
	}
	stored, err := db.NewSelect().Model((*TTYFile)(nil)).Where("sha256 = ?", *s.TTYSHA256).Exists(ctx)
	if err != nil {
		return nil, err
	}
	if !stored {
		return nil, ErrNoRecording
	}

	rec := &Recording{Session: s.Session, SHA256: *s.TTYSHA256}
	var raw []RawEvent
	if err := db.NewSelect().Model(&raw).Column("ts", "data").
		Where("session_id = ?", id).Where("eventid = ?", "cowrie.client.size").
		Order("ts", "id").Scan(ctx); err != nil {
		return nil, err
	}
	for _, e := range raw {
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
