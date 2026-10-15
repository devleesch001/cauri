package asciicast

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestV3(t *testing.T) {
	var sb strings.Builder
	enc := NewV3(&sb)
	start := time.Unix(1760432400, 0)
	if err := enc.WriteHeader(Header{Cols: 80, Rows: 24, Timestamp: start, IdleTimeLimit: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range []Event{
		{Time: 0, Code: Output, Data: "$ "},
		{Time: 1500 * time.Millisecond, Code: Input, Data: "l"},
		{Time: 1500*time.Millisecond + 7*time.Microsecond, Code: Output, Data: "<l>\x1b[0mé"},
		{Time: time.Second, Code: Output, Data: "late"}, // out of order: interval 0
		{Time: 3 * time.Second, Code: Resize, Data: ResizeData(120, 40)},
	} {
		if err := enc.WriteEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}

	want := `{"version":3,"term":{"cols":80,"rows":24},"timestamp":1760432400,"idle_time_limit":2}
[0, "o", "$ "]
[1.5, "i", "l"]
[0.000007, "o", "<l>\u001b[0mé"]
[0, "o", "late"]
[1.499993, "r", "120x40"]
`
	if sb.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", sb.String(), want)
	}
	for _, line := range strings.Split(strings.TrimSpace(sb.String()), "\n") {
		if !json.Valid([]byte(line)) {
			t.Errorf("invalid JSON line %s", line)
		}
	}
}

func TestV3OptionalHeaderFields(t *testing.T) {
	var sb strings.Builder
	enc := NewV3(&sb)
	_ = enc.WriteHeader(Header{Cols: 80, Rows: 24})
	_ = enc.Flush()
	if got := sb.String(); got != `{"version":3,"term":{"cols":80,"rows":24}}`+"\n" {
		t.Fatalf("header = %s", got)
	}
}

func TestSimulate(t *testing.T) {
	var b strings.Builder
	err := Simulate(NewV3(&b), Header{Cols: 80, Rows: 24}, "# ", []Step{
		{Time: 0, Command: "uname -a"},
		{Time: 1500 * time.Millisecond, Command: "echo a\necho b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"version":3,"term":{"cols":80,"rows":24}}
[0, "m", "uname -a"]
[0, "o", "# uname -a\r\n"]
[1.5, "m", "echo a\necho b"]
[0, "o", "# echo a\r\necho b\r\n"]
`
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
