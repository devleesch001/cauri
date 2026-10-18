package ttylog

import (
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/devleesch001/cauri/internal/asciicast"
)

// TermSize is a terminal size reported at some time, from Cowrie's
// cowrie.client.size events: the recording itself does not hold it.
type TermSize struct {
	Time       time.Time
	Cols, Rows int
}

// Options configure a conversion.
type Options struct {
	// Sizes are the sizes reported during the session, in time order.
	// The one in effect at the start sizes the terminal; later ones become
	// resize events. Without any, the terminal is DefaultCols×DefaultRows.
	Sizes []TermSize
	// IdleTimeLimit is written in the header as a playback hint.
	IdleTimeLimit time.Duration
	// Commands typed during the session, in time order, from Cowrie's
	// cowrie.command.input events: each becomes a marker (a chapter).
	Commands []Command
	// Prompt is shown, faint, before the command of an exec channel: such
	// a channel has no shell, so no prompt and no echo, and the terminal
	// would show its output alone. Cowrie records the command whole
	// (DirInteract); it is shown as if typed at a shell.
	Prompt string
}

// Command is a command typed in the session, at the time Cowrie ran it.
type Command struct {
	Time  time.Time
	Input string
}

// maxMarkerLabel bounds marker labels: they come from the attacker.
const maxMarkerLabel = 200

// Default terminal size.
const (
	DefaultCols = 80
	DefaultRows = 24
)

// Convert writes the recording read from r to enc: output as Output
// events, typed input as Input events, at their real times. Nothing is
// written if the first record cannot be read. A recording damaged further
// on is converted up to the damage, and the error is returned.
func Convert(r io.Reader, enc asciicast.Encoder, opts Options) error {
	rd := NewReader(r)
	first, err := rd.Next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return ErrTruncated
		}
		return err
	}
	start := first.Time

	// The client reports its size around the opening of the shell, just
	// before or just after the first record: the first size reported, or
	// the last one before the start, sizes the terminal.
	cols, rows := DefaultCols, DefaultRows
	sizes := validSizes(opts.Sizes)
	if len(sizes) > 0 {
		cols, rows = sizes[0].Cols, sizes[0].Rows
		sizes = sizes[1:]
	}
	for len(sizes) > 0 && !sizes[0].Time.After(start) {
		cols, rows = sizes[0].Cols, sizes[0].Rows
		sizes = sizes[1:]
	}
	if err := enc.WriteHeader(asciicast.Header{
		Cols: cols, Rows: rows, Timestamp: start, IdleTimeLimit: opts.IdleTimeLimit,
	}); err != nil {
		return err
	}

	c := converter{enc: enc, start: start, sizes: sizes, commands: opts.Commands, prompt: opts.Prompt}
	rec := first
	for {
		if err := c.record(rec); err != nil {
			return err
		}
		rec, err = rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// Keep what could be read, then report the damage.
			if ferr := c.finish(); ferr != nil {
				return ferr
			}
			return err
		}
	}
	return c.finish()
}

// MaxTermSize bounds the columns and rows accepted from a client.
const MaxTermSize = 1000

// validSizes drops sizes a client could report to break players.
func validSizes(sizes []TermSize) []TermSize {
	var out []TermSize
	for _, s := range sizes {
		if s.Cols > 0 && s.Rows > 0 && s.Cols <= MaxTermSize && s.Rows <= MaxTermSize {
			out = append(out, s)
		}
	}
	return out
}

type converter struct {
	enc        asciicast.Encoder
	start      time.Time
	last       time.Duration
	sizes      []TermSize
	commands   []Command
	prompt     string
	out, input utf8Buffer
}

func (c *converter) record(rec Record) error {
	at := max(rec.Time.Sub(c.start), 0)
	c.last = max(c.last, at)
	if err := c.resizesUntil(rec.Time); err != nil {
		return err
	}
	if err := c.markersUntil(rec.Time); err != nil {
		return err
	}
	if rec.Op != OpWrite {
		return nil
	}
	switch rec.Dir {
	case DirOutput:
		return c.emit(at, asciicast.Output, onlcr(c.out.decode(rec.Data)))
	case DirInput:
		return c.emit(at, asciicast.Input, c.input.decode(rec.Data))
	case DirInteract:
		cmd := strings.TrimRight(strings.ToValidUTF8(string(rec.Data), string(utf8.RuneError)), "\r\n")
		return c.emit(at, asciicast.Output, faint(c.prompt)+onlcr(cmd)+"\r\n")
	}
	return nil
}

// faint dims text added by cauri, which the terminal did not show.
func faint(s string) string {
	if s == "" {
		return ""
	}
	return "\x1b[2m" + s + "\x1b[22m"
}

func (c *converter) resizesUntil(t time.Time) error {
	for len(c.sizes) > 0 && !c.sizes[0].Time.After(t) {
		s := c.sizes[0]
		c.sizes = c.sizes[1:]
		if err := c.enc.WriteEvent(asciicast.Event{
			Time: max(s.Time.Sub(c.start), 0), Code: asciicast.Resize, Data: asciicast.ResizeData(s.Cols, s.Rows),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *converter) markersUntil(t time.Time) error {
	for len(c.commands) > 0 && !c.commands[0].Time.After(t) {
		if err := c.marker(c.commands[0]); err != nil {
			return err
		}
		c.commands = c.commands[1:]
	}
	return nil
}

func (c *converter) marker(cmd Command) error {
	label := cmd.Input
	if r := []rune(label); len(r) > maxMarkerLabel {
		label = string(r[:maxMarkerLabel]) + "…"
	}
	return c.enc.WriteEvent(asciicast.Event{
		Time: max(cmd.Time.Sub(c.start), 0), Code: asciicast.Marker, Data: label,
	})
}

// finish writes bytes still waiting for the rest of a character, the
// markers of commands logged after the last record (such as "exit"), and
// flushes the encoder. Sizes reported after the end are dropped.
func (c *converter) finish() error {
	if err := c.emit(c.last, asciicast.Output, onlcr(c.out.flush())); err != nil {
		return err
	}
	if err := c.emit(c.last, asciicast.Input, c.input.flush()); err != nil {
		return err
	}
	for _, cmd := range c.commands {
		if err := c.marker(cmd); err != nil {
			return err
		}
	}
	c.commands = nil
	return c.enc.Flush()
}

func (c *converter) emit(at time.Duration, code, data string) error {
	if data == "" {
		return nil
	}
	return c.enc.WriteEvent(asciicast.Event{Time: at, Code: code, Data: data})
}

// onlcr does what the terminal driver does to output on a real terminal
// (stty onlcr): Cowrie records its shell's output before that, with bare
// "\n" line ends. A "\r\n" becomes "\r\r\n", which displays the same.
func onlcr(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

// utf8Buffer turns a byte stream cut at arbitrary places into valid UTF-8
// strings: a character split between two writes is kept until complete,
// invalid bytes become U+FFFD.
type utf8Buffer struct {
	pending []byte
}

func (b *utf8Buffer) decode(p []byte) string {
	data := append(b.pending, p...)
	cut := len(data)
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				cut = i
			}
			break
		}
	}
	b.pending = append([]byte(nil), data[cut:]...)
	return strings.ToValidUTF8(string(data[:cut]), string(utf8.RuneError))
}

func (b *utf8Buffer) flush() string {
	s := strings.ToValidUTF8(string(b.pending), string(utf8.RuneError))
	b.pending = nil
	return s
}
