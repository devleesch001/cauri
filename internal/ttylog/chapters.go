package ttylog

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Chapter is a command of a session and what it printed.
type Chapter struct {
	// Time is the offset of the command from the start of the recording,
	// the same as its marker in the replay.
	Time    time.Duration
	Command string
	// Output is the plain text the command printed: no colours or other
	// terminal sequences, no echo of the command, no final prompt.
	Output string
	// Truncated means Output stops at MaxChapterOutput bytes.
	Truncated bool
	// FullScreen means the command drew a full-screen program (an editor,
	// a pager): its output is not meaningful as text and is left empty.
	FullScreen bool
}

// Summary describes a recording and its chapters.
type Summary struct {
	Records  int // records in the recording
	Chapters []Chapter
}

// MaxChapterOutput bounds the output kept per chapter, in bytes of raw
// terminal output: it comes from the attacker.
const MaxChapterOutput = 64 << 10

// enterWindow is how far from the time Cowrie logged a command its Enter
// key may be: they differ by under a millisecond on real recordings.
const enterWindow = 2 * time.Second

// Chapters splits a recording by the commands typed in it, in time order.
//
// A command's output is what the terminal printed after the echo of the
// command line (up to the first line feed after its Enter key), until the
// next key typed or the next command, without the prompt that ends it.
// The recording is read twice, hence the io.ReadSeeker.
func Chapters(r io.ReadSeeker, cmds []Command) (Summary, error) {
	var sum Summary

	// First pass: the input records, to find each command's Enter key.
	var start time.Time
	var inputs []inputRecord
	rd := NewReader(r)
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if sum.Records == 0 {
				return sum, err
			}
			break // damaged: chapters up to the damage
		}
		if sum.Records == 0 {
			start = rec.Time
		}
		sum.Records++
		if rec.Op == OpWrite && rec.Dir == DirInput {
			inputs = append(inputs, inputRecord{rec.Time, strings.ContainsAny(string(rec.Data), "\r\n")})
		}
	}
	if sum.Records == 0 {
		return sum, ErrTruncated
	}
	if len(cmds) == 0 {
		return sum, nil
	}

	segs := make([]segment, len(cmds))
	for i, cmd := range cmds {
		segs[i].enter = enterTime(inputs, cmd.Time)
		segs[i].Chapter = Chapter{Time: max(cmd.Time.Sub(start), 0), Command: cmd.Input}
	}

	// Second pass: give each output record to the chapter it belongs to.
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return sum, err
	}
	rd = NewReader(r)
	cur := -1
	for {
		rec, err := rd.Next()
		if err != nil {
			break // io.EOF, or the damage already met in the first pass
		}
		for cur+1 < len(segs) && !segs[cur+1].enter.After(rec.Time) {
			cur++
		}
		if cur < 0 || rec.Op != OpWrite {
			continue
		}
		segs[cur].add(rec)
	}

	sum.Chapters = make([]Chapter, len(segs))
	for i := range segs {
		sum.Chapters[i] = segs[i].finish()
	}
	return sum, nil
}

type inputRecord struct {
	time  time.Time
	enter bool // holds a line end: the Enter key
}

// enterTime finds the Enter key of a command logged at t: the input record
// holding a line end closest to t, or t itself if none is near.
func enterTime(inputs []inputRecord, t time.Time) time.Time {
	best, bestGap := t, enterWindow
	for _, in := range inputs {
		if !in.enter {
			continue
		}
		gap := in.time.Sub(t)
		if gap < 0 {
			gap = -gap
		}
		if gap <= bestGap {
			best, bestGap = in.time, gap
		}
	}
	return best
}

// segment collects the output of one chapter.
type segment struct {
	Chapter
	enter    time.Time
	echoDone bool // the line feed ending the echo of the command was seen
	stopped  bool // a key was typed after the output: the next command
	raw      []byte
}

func (s *segment) add(rec Record) {
	if s.stopped {
		return
	}
	if rec.Dir == DirInput {
		// Keys typed before the echo ended belong to this command line.
		if s.echoDone {
			s.stopped = true
		}
		return
	}
	if rec.Dir != DirOutput {
		return
	}
	data := rec.Data
	if !s.echoDone {
		i := strings.IndexByte(string(data), '\n')
		if i < 0 {
			return // still the echo
		}
		s.echoDone = true
		data = data[i+1:]
	}
	if room := MaxChapterOutput - len(s.raw); len(data) > room {
		data = data[:room]
		s.Truncated = true
		s.stopped = true
	}
	s.raw = append(s.raw, data...)
}

func (s *segment) finish() Chapter {
	text, full := plainText(s.raw)
	ch := s.Chapter
	if full {
		ch.FullScreen = true
		return ch
	}
	// The last line, not ended by a line feed, is the prompt that follows
	// the output.
	if i := strings.LastIndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	} else {
		text = ""
	}
	ch.Output = strings.Trim(text, "\n")
	return ch
}

// plainText renders terminal output as text: escape sequences removed,
// carriage returns and backspaces applied to the current line, other
// control characters dropped. It reports whether the output switched to
// the alternate screen used by full-screen programs.
func plainText(raw []byte) (string, bool) {
	s := strings.ToValidUTF8(string(raw), string(utf8.RuneError))
	var out strings.Builder
	var line []rune
	full := false
	flush := func() {
		out.WriteString(string(line))
		out.WriteByte('\n')
		line = line[:0]
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == 0x1b:
			n, alt := escapeLen(s[i:])
			full = full || alt
			i += n
			continue
		case r == '\n':
			flush()
		case r == '\r':
			if i+1 < len(s) && s[i+1] == '\n' {
				break // the line feed follows
			}
			line = line[:0] // back to the start of the line: overwritten
		case r == '\b':
			if len(line) > 0 {
				line = line[:len(line)-1]
			}
		case r == '\t':
			line = append(line, r)
		case r < 0x20 || r == 0x7f:
			// other control characters print nothing
		default:
			line = append(line, r)
		}
		i += size
	}
	out.WriteString(string(line))
	return out.String(), full
}

// escapeLen returns the length of the escape sequence at the start of s
// (which starts with ESC), and whether it switches to the alternate screen.
func escapeLen(s string) (int, bool) {
	if len(s) < 2 {
		return len(s), false
	}
	switch s[1] {
	case '[': // CSI: parameters, then a final byte in @..~
		for j := 2; j < len(s); j++ {
			if c := s[j]; c >= 0x40 && c <= 0x7e {
				params := s[2:j]
				alt := c == 'h' && (params == "?1049" || params == "?47" || params == "?1047")
				return j + 1, alt
			}
		}
		return len(s), false
	case ']': // OSC: until BEL or ESC \
		for j := 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1, false
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2, false
			}
		}
		return len(s), false
	case '(', ')', '#', '%': // character set and similar: one more byte
		return min(3, len(s)), false
	default:
		return 2, false
	}
}

// String describes a chapter for logs and test failures.
func (c Chapter) String() string {
	return fmt.Sprintf("%v %q -> %q (truncated %v, full screen %v)", c.Time, c.Command, c.Output, c.Truncated, c.FullScreen)
}
