// Package asciicast writes terminal recordings in the asciicast format
// read by asciinema-player.
//
// Producers describe a recording with Header and Event, whose times are
// absolute offsets from the start; each format version is an Encoder
// that turns them into its own encoding. Version 3 is the only one today.
package asciicast

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Header describes a recording.
type Header struct {
	Cols, Rows int
	// Timestamp is the start of the recording; zero if unknown.
	Timestamp time.Time
	// IdleTimeLimit asks players to shorten pauses longer than it; zero
	// means none. It is a playback hint: event times stay real.
	IdleTimeLimit time.Duration
}

// Event codes.
const (
	Output = "o" // data written to the terminal
	Input  = "i" // data sent by the user, keystrokes
	Resize = "r" // new terminal size, as "COLSxROWS"
)

// Event is something that happened at Time after the start.
type Event struct {
	Time time.Duration
	Code string
	Data string
}

// ResizeData returns the Data of a Resize event.
func ResizeData(cols, rows int) string { return fmt.Sprintf("%dx%d", cols, rows) }

// Encoder writes a recording: the header once, then events in time order.
type Encoder interface {
	WriteHeader(Header) error
	WriteEvent(Event) error
	// Flush writes buffered data to the underlying writer.
	Flush() error
}

// ContentType is the media type of asciicast files.
const ContentType = "application/x-asciicast"

// NewV3 returns an encoder for asciicast version 3.
func NewV3(w io.Writer) Encoder { return &v3{w: bufio.NewWriter(w)} }

type v3 struct {
	w    *bufio.Writer
	last time.Duration
}

type v3Header struct {
	Version       int     `json:"version"`
	Term          v3Term  `json:"term"`
	Timestamp     int64   `json:"timestamp,omitempty"`
	IdleTimeLimit float64 `json:"idle_time_limit,omitempty"`
}

type v3Term struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func (e *v3) WriteHeader(h Header) error {
	hd := v3Header{Version: 3, Term: v3Term{Cols: h.Cols, Rows: h.Rows}, IdleTimeLimit: h.IdleTimeLimit.Seconds()}
	if !h.Timestamp.IsZero() {
		hd.Timestamp = h.Timestamp.Unix()
	}
	b, err := json.Marshal(hd)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.w, "%s\n", b)
	return err
}

// WriteEvent writes the interval since the previous event, in whole
// microseconds: no rounding error accumulates over long recordings.
func (e *v3) WriteEvent(ev Event) error {
	interval := max(ev.Time-e.last, 0)
	e.last = max(ev.Time, e.last)
	data, err := jsonString(ev.Data)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(e.w, "[%s, %q, %s]\n", seconds(interval), ev.Code, data)
	return err
}

func (e *v3) Flush() error { return e.w.Flush() }

// seconds formats d with microsecond precision, without trailing zeros.
func seconds(d time.Duration) string {
	us := d.Microseconds()
	s := fmt.Sprintf("%d.%06d", us/1e6, us%1e6)
	return strings.TrimSuffix(strings.TrimRight(s, "0"), ".")
}

// jsonString encodes s as a JSON string, leaving <, > and & as is.
func jsonString(s string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
