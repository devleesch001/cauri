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
type cowrieEvent struct {
	EventID   string  `json:"eventid"`
	Timestamp string  `json:"timestamp"`
	Session   string  `json:"session"`
	SrcIP     *string `json:"src_ip"`
	SrcPort   *int    `json:"src_port"`
	DstIP     *string `json:"dst_ip"`
	DstPort   *int    `json:"dst_port"`
	Protocol  *string `json:"protocol"`

	Version *string `json:"version"` // client.version
	HASSH   *string `json:"hassh"`   // client.kex

	Username *string `json:"username"` // login.*
	Password *string `json:"password"`

	Input *string `json:"input"` // command.input

	URL      *string `json:"url"`      // file_download
	DestFile *string `json:"destfile"` // file_download: path asked by the client
	Filename *string `json:"filename"` // file_upload
	Shasum   *string `json:"shasum"`   // file_*, log.closed

	TTYLog   *string    `json:"ttylog"` // log.closed
	Size     *int64     `json:"size"`
	Duration *flexFloat `json:"duration"` // log.closed, session.closed

	ts time.Time
}

// flexFloat accepts a JSON number or a string holding one: depending on its
// version, Cowrie writes durations either way.
type flexFloat float64

func (f *flexFloat) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*f = flexFloat(v)
	return nil
}

func (f *flexFloat) ptr() *float64 {
	if f == nil {
		return nil
	}
	v := float64(*f)
	return &v
}

// parseEvent validates a raw cowrie.json object and extracts its fields.
func parseEvent(raw []byte) (*cowrieEvent, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, errors.New("not a JSON object")
	}
	var ev cowrieEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		return nil, err
	}
	if ev.EventID == "" {
		return nil, errors.New("missing eventid")
	}
	ts, err := time.Parse(time.RFC3339Nano, ev.Timestamp)
	if err != nil {
		return nil, errors.New("missing or invalid timestamp")
	}
	ev.ts = ts.UTC()
	ev.sanitize()
	return &ev, nil
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

// ttySHA256 returns the digest of the TTY recording named by a log.closed
// event: the shasum field, or the file name, which Cowrie sets to the digest.
func (ev *cowrieEvent) ttySHA256() *string {
	if ev.Shasum != nil && *ev.Shasum != "" {
		s := strings.ToLower(*ev.Shasum)
		return &s
	}
	if ev.TTYLog != nil && *ev.TTYLog != "" {
		s := strings.ToLower(path.Base(*ev.TTYLog))
		return &s
	}
	return nil
}
