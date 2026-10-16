package ingest

import (
	"encoding/json"
	"errors"
	"math"
	"path"
	"strconv"
	"time"

	"github.com/devleesch001/cauri/internal/cowrie"
)

// Cowrie event identifiers projected into dedicated tables (package
// cowrie lists the fields each one stores).
const (
	evConnect       = cowrie.Connect
	evClientVersion = cowrie.ClientVersion
	evClientKex     = cowrie.ClientKex
	evLoginSuccess  = cowrie.LoginSuccess
	evLoginFailed   = cowrie.LoginFailed
	evCommandInput  = cowrie.CommandInput
	evFileDownload  = cowrie.FileDownload
	evFileUpload    = cowrie.FileUpload
	evLogClosed     = cowrie.LogClosed
	evSessionClosed = cowrie.SessionClosed
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

	Version *string     // client.version
	Kex     *cowrie.Kex // client.kex

	Username *string // login.*
	Password *string

	Input *string // command.input

	URL      *string // file_download
	DestFile *string // file_download: path asked by the client
	Filename *string // file_upload
	Shasum   *string // file_*, log.closed

	TTYLog *string // log.closed
	Size   *int64
	// log.closed and session.closed, in milliseconds: Cowrie 3 writes
	// duration_ms, older versions duration in seconds, as a number or a
	// string.
	durationMS *int64

	// data is what the tables do not hold of the line (cowrie.Fragment).
	data *string

	ts time.Time
}

// parseEvent validates a raw cowrie.json object and extracts its fields.
func parseEvent(raw []byte) (*cowrieEvent, error) {
	f, ok := cowrie.Parse(raw)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	m := fields(f)
	ev := &cowrieEvent{
		SrcIP: m.str("src_ip"), SrcPort: m.int("src_port"),
		DstIP: m.str("dst_ip"), DstPort: m.int("dst_port"), Protocol: m.str("protocol"),
		Version:  m.str("version"),
		Username: m.str("username"), Password: m.str("password"), Input: m.str("input"),
		URL: m.str("url"), DestFile: m.str("destfile"), Filename: m.str("filename"), Shasum: m.str("shasum"),
		TTYLog: m.str("ttylog"), Size: m.int64("size"),
	}
	if ms := m.float("duration_ms"); ms != nil {
		ev.durationMS = millis(*ms)
	} else if sec := m.float("duration"); sec != nil {
		ev.durationMS = millis(*sec * 1000)
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
	if ev.EventID == evClientKex {
		k := cowrie.ParseKex(f)
		ev.Kex = &k
	}
	ev.data = cowrie.Fragment(f, ev.EventID, ev.Session != "")
	return ev, nil
}

// millis rounds a duration in milliseconds; nil if out of range.
func millis(ms float64) *int64 {
	if math.IsNaN(ms) || ms < 0 || ms > math.MaxInt64/2 {
		return nil
	}
	v := int64(math.Round(ms))
	return &v
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
		ev.SrcIP, ev.DstIP, ev.Protocol, ev.Version, ev.Username,
		ev.Password, ev.Input, ev.URL, ev.DestFile, ev.Filename, ev.Shasum, ev.TTYLog,
	} {
		if p != nil {
			*p = cleanText(*p)
		}
	}
	ev.EventID = cleanText(ev.EventID)
	ev.Session = cleanText(ev.Session)
}

func cleanText(s string) string { return cowrie.CleanText(s) }

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
