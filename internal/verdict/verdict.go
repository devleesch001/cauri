// Package verdict tells whether a session looks typed by a human or run
// by a bot, from how its keys were typed (the TTY recording) and from its
// events. It adds points for signals of either kind; the verdict and every
// reason are shown, so that an analyst can judge the heuristic.
//
// The thresholds come from a small sample (scripted and interactive
// sessions against the official Cowrie image): they are grouped below to
// be tuned against real attacks.
package verdict

import (
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/devleesch001/cauri/internal/ttylog"
)

// Verdict is the conclusion for a session.
type Verdict string

const (
	Human         Verdict = "human"
	Bot           Verdict = "bot"
	Uncertain     Verdict = "uncertain"
	NoInteraction Verdict = "no_interaction" // no command, no recording: nothing to judge
)

// Reason codes, each with the points it adds (positive: human).
const (
	KeyByKey          = "types_key_by_key"
	WholeLines        = "sends_whole_lines"
	IrregularTyping   = "irregular_typing"
	MechanicalTyping  = "mechanical_typing"
	HumanTypingSpeed  = "human_typing_speed"
	TooFastToType     = "too_fast_to_type"
	EditsCommands     = "edits_commands"
	IrregularPauses   = "irregular_pauses"
	ConstantDelay     = "constant_delay"
	NoTerminal        = "no_terminal"
	AutomationLibrary = "automation_library"
)

// Thresholds.
const (
	humanScore = 3  // at least: human
	botScore   = -3 // at most: bot

	keyByKeyShare   = 0.8 // share of input sent key by key
	wholeLinesShare = 0.2
	minInputs       = 3 // input records needed to judge granularity

	minKeyGaps         = 10   // intervals between keys needed to judge typing
	irregularTypingCV  = 0.4  // coefficient of variation of key intervals
	mechanicalTypingCV = 0.1  //
	humanSpeedMin      = 60 * time.Millisecond
	humanSpeedMax      = 600 * time.Millisecond
	tooFastToType      = 20 * time.Millisecond

	minPauses        = 3 // pauses between commands needed to judge them
	humanPauseMedian = 500 * time.Millisecond
	irregularPauseCV = 0.3
	constantPauseCV  = 0.05

	maxSamples = 10_000 // intervals kept per kind: bounds memory
)

// automationClient matches SSH client versions of libraries used by
// scripts, after "SSH-2.0-". Bots also use OpenSSH: it is not listed.
// The name is followed by a version ("libssh_0.9.6", "JSCH-0.1.54") or
// nothing ("Go"); "_" is a word character, so \b cannot end it.
var automationClient = regexp.MustCompile(`(?i)^SSH-[\d.]+-(Go|libssh|libssh2|paramiko|AsyncSSH|JSCH|Twisted|russh|ssh2js|SSHJ|Renci\.SshNet|sshlib)([_\-. ]|$)`)

// Reason is a signal found in a session.
type Reason struct {
	Code   string
	Points int
}

// Result is the verdict for a session.
type Result struct {
	Verdict Verdict
	Score   int
	Reasons []Reason
}

// Facts are what the events of a session tell.
type Facts struct {
	// Terminal: the session had a pty (a recording or a terminal size).
	Terminal      bool
	Commands      int    // commands typed or run
	ClientVersion string // as announced, such as "SSH-2.0-Go"
}

// Typing is what the recording of a session tells about its input.
type Typing struct {
	Inputs   int             // input records
	KeyByKey int             // input records holding a single key
	Edits    int             // backspace, arrows, Tab, Ctrl-C
	KeyGaps  []time.Duration // between keys of a same line
	Pauses   []time.Duration // from an Enter key to the next key
}

// ReadTyping reads the input of a recording, in one pass and bounded
// memory. A damaged recording is read up to the damage.
func ReadTyping(r io.Reader) (Typing, error) {
	var ty Typing
	rd := ttylog.NewReader(r)
	var last time.Time
	lastEnter, lastKey, seen := false, false, false
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return ty, nil
		}
		if err != nil {
			if ty.Inputs == 0 {
				return ty, err
			}
			return ty, nil
		}
		if rec.Op != ttylog.OpWrite || rec.Dir != ttylog.DirInput || len(rec.Data) == 0 {
			continue
		}
		ty.Inputs++
		key := singleKey(rec.Data)
		if key {
			ty.KeyByKey++
		}
		ty.Edits += edits(rec.Data)
		if seen {
			gap := max(rec.Time.Sub(last), 0)
			switch {
			case lastEnter:
				ty.Pauses = appendSample(ty.Pauses, gap)
			case key && lastKey:
				ty.KeyGaps = appendSample(ty.KeyGaps, gap)
			}
		}
		last, seen = rec.Time, true
		lastKey = key
		lastEnter = strings.ContainsAny(string(rec.Data), "\r\n")
	}
}

// Judge adds up the signals of a session. typing is nil without a
// recording.
func Judge(f Facts, typing *Typing) Result {
	var res Result
	add := func(code string, points int) {
		res.Reasons = append(res.Reasons, Reason{code, points})
		res.Score += points
	}

	if typing != nil && typing.Inputs >= minInputs {
		share := float64(typing.KeyByKey) / float64(typing.Inputs)
		switch {
		case share >= keyByKeyShare:
			add(KeyByKey, 2)
		case share <= wholeLinesShare:
			add(WholeLines, -2)
		}
	}
	if typing != nil && len(typing.KeyGaps) >= minKeyGaps {
		switch cv := variation(typing.KeyGaps); {
		case cv >= irregularTypingCV:
			add(IrregularTyping, 1)
		case cv < mechanicalTypingCV:
			add(MechanicalTyping, -2)
		}
		switch m := median(typing.KeyGaps); {
		case m >= humanSpeedMin && m <= humanSpeedMax:
			add(HumanTypingSpeed, 1)
		case m < tooFastToType:
			add(TooFastToType, -1)
		}
	}
	if typing != nil && typing.Edits > 0 {
		add(EditsCommands, 2)
	}
	if typing != nil && len(typing.Pauses) >= minPauses {
		cv := variation(typing.Pauses)
		switch {
		case median(typing.Pauses) >= humanPauseMedian && cv >= irregularPauseCV:
			add(IrregularPauses, 1)
		case cv < constantPauseCV:
			add(ConstantDelay, -2)
		}
	}
	if f.Commands > 0 && !f.Terminal {
		add(NoTerminal, -2)
	}
	if automationClient.MatchString(f.ClientVersion) {
		add(AutomationLibrary, -1)
	}

	switch {
	case f.Commands == 0 && typing == nil:
		res.Verdict = NoInteraction
	case res.Score >= humanScore:
		res.Verdict = Human
	case res.Score <= botScore:
		res.Verdict = Bot
	default:
		res.Verdict = Uncertain
	}
	return res
}

// singleKey reports whether an input record holds one key: a character,
// or the escape sequence of a special key (an arrow is "\x1b[A").
func singleKey(b []byte) bool {
	if len(b) == 1 {
		return true
	}
	if b[0] == 0x1b && len(b) <= 6 {
		return true
	}
	// One multi-byte UTF-8 character.
	return len(b) <= 4 && len([]rune(string(b))) == 1
}

// edits counts the keys that correct or complete a command line.
func edits(b []byte) int {
	n := 0
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case 0x7f, 0x08, '\t', 0x03: // backspace, Tab, Ctrl-C
			n++
		case 0x1b:
			if i+2 < len(b) && b[i+1] == '[' && strings.IndexByte("ABCD", b[i+2]) >= 0 {
				n++ // an arrow key
				i += 2
			}
		}
	}
	return n
}

func appendSample(s []time.Duration, d time.Duration) []time.Duration {
	if len(s) >= maxSamples {
		return s
	}
	return append(s, d)
}

func median(s []time.Duration) time.Duration {
	c := slices.Clone(s)
	slices.Sort(c)
	return c[len(c)/2]
}

// variation is the coefficient of variation: standard deviation over mean.
func variation(s []time.Duration) float64 {
	var mean float64
	for _, d := range s {
		mean += d.Seconds()
	}
	mean /= float64(len(s))
	if mean == 0 {
		return 0
	}
	var sq float64
	for _, d := range s {
		sq += (d.Seconds() - mean) * (d.Seconds() - mean)
	}
	return math.Sqrt(sq/float64(len(s))) / mean
}
