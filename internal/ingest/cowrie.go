package ingest

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Cowrie event identifiers projected into dedicated tables. Every other
// event is only kept in raw_events.
const (
	evConnect       = "cowrie.session.connect"
	evClientVersion = "cowrie.client.version"
	evClientKex     = "cowrie.client.kex"
	evLoginSuccess  = "cowrie.login.success"
	evLoginFailed   = "cowrie.login.failed"
	evCommandInput  = "cowrie.command.input"
	evFileDownload  = "cowrie.session.file_download"
	evFileUpload    = "cowrie.session.file_upload"
	evLogClosed     = "cowrie.log.closed"
	evSessionClosed = "cowrie.session.closed"
)

// cowrieEvent holds the fields of a cowrie.json line that cauri uses.
// A field absent, null or of an unexpected type is left nil: the event is
// stored raw anyway, so a format change in Cowrie costs a column, not data.
type cowrieEvent struct {
	EventID  string
	Session  string
	SrcIP    *string
	SrcPort  *int
	DstIP    *string
	DstPort  *int
	Protocol *string

	Version *string // client.version
	HASSH   *string // client.kex

	Username *string // login.*
	Password *string

	Input *string // command.input

	URL      *string // file_download
	DestFile *string // file_download: path asked by the client
	Filename *string // file_upload
	Shasum   *string // file_*, log.closed

	TTYLog *string // log.closed
	Size   *int64
	// log.closed and session.closed: Cowrie 3 writes duration_ms, older
	// versions duration in seconds, as a number or a string.
	durationSeconds *float64

	ts time.Time
}

// seconds returns the duration of the event in seconds, if it has one.
func (ev *cowrieEvent) seconds() *float64 { return ev.durationSeconds }

// parseEvent validates a raw cowrie.json object and extracts its fields.
func parseEvent(raw []byte) (*cowrieEvent, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errors.New("not a JSON object")
	}
	var m fields
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	ev := &cowrieEvent{
		SrcIP: m.str("src_ip"), SrcPort: m.int("src_port"),
		DstIP: m.str("dst_ip"), DstPort: m.int("dst_port"), Protocol: m.str("protocol"),
		Version: m.str("version"), HASSH: m.str("hassh"),
		Username: m.str("username"), Password: m.str("password"), Input: m.str("input"),
		URL: m.str("url"), DestFile: m.str("destfile"), Filename: m.str("filename"), Shasum: m.str("shasum"),
		TTYLog: m.str("ttylog"), Size: m.int64("size"),
	}
	if ms := m.float("duration_ms"); ms != nil {
		v := *ms / 1000
		ev.durationSeconds = &v
	} else {
		ev.durationSeconds = m.float("duration")
	}
	if p := m.str("eventid"); p != nil {
		ev.EventID = *p
	}
	if ev.EventID == "" {
		return nil, errors.New("missing eventid")
	}
	if p := m.str("session"); p != nil {
		ev.Session = *p
	}
	var ts time.Time
	var err error
	if p := m.str("timestamp"); p != nil {
		ts, err = time.Parse(time.RFC3339Nano, *p)
	}
	if ts.IsZero() || err != nil {
		return nil, errors.New("missing or invalid timestamp")
	}
	ev.ts = ts.UTC()
	ev.sanitize()
	return ev, nil
}

// fields are the members of a JSON object, decoded on demand.
type fields map[string]json.RawMessage

func get[T any](m fields, key string) *T {
	raw, ok := m[key]
	if !ok || string(raw) == "null" {
		return nil
	}
	v := new(T)
	if json.Unmarshal(raw, v) != nil {
		return nil
	}
	return v
}

func (m fields) str(key string) *string  { return get[string](m, key) }
func (m fields) int(key string) *int     { return get[int](m, key) }
func (m fields) int64(key string) *int64 { return get[int64](m, key) }

// float accepts a number or a string holding one.
func (m fields) float(key string) *float64 {
	if v := get[float64](m, key); v != nil {
		return v
	}
	if s := m.str(key); s != nil {
		if v, err := strconv.ParseFloat(*s, 64); err == nil {
			return &v
		}
	}
	return nil
}

// sanitize makes the extracted strings storable in every database:
// PostgreSQL rejects NUL bytes and invalid UTF-8 in TEXT columns, and
// attackers do send them (as usernames, passwords or commands).
func (ev *cowrieEvent) sanitize() {
	for _, p := range []*string{
		ev.SrcIP, ev.DstIP, ev.Protocol, ev.Version, ev.HASSH, ev.Username,
		ev.Password, ev.Input, ev.URL, ev.DestFile, ev.Filename, ev.Shasum, ev.TTYLog,
	} {
		if p != nil {
			*p = cleanText(*p)
		}
	}
	ev.EventID = cleanText(ev.EventID)
	ev.Session = cleanText(ev.Session)
}

func cleanText(s string) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	return strings.ReplaceAll(s, "\x00", string(utf8.RuneError))
}

// ttyName returns the file name of the TTY recording of a log.closed
// event. Cowrie derives it from a hash of the terminal output, not of the
// file: it names the recording but cannot check it.
func (ev *cowrieEvent) ttyName() *string {
	if ev.TTYLog == nil || *ev.TTYLog == "" {
		return nil
	}
	s := path.Base(*ev.TTYLog)
	return &s
}
