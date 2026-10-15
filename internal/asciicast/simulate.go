package asciicast

import (
	"strings"
	"time"
)

// Step is a command typed at Time after the start of a simulated replay.
type Step struct {
	Time    time.Duration
	Command string
}

// maxMarkerLabel bounds marker labels: commands come from attackers.
const maxMarkerLabel = 200

// Simulate writes a replay made of commands only, without the output the
// terminal gave: a marker per step, then the prompt and the command at the
// step's time. It is for sessions whose recording is not available.
func Simulate(enc Encoder, h Header, prompt string, steps []Step) error {
	if err := enc.WriteHeader(h); err != nil {
		return err
	}
	for _, s := range steps {
		label := s.Command
		if r := []rune(label); len(r) > maxMarkerLabel {
			label = string(r[:maxMarkerLabel]) + "…"
		}
		if err := enc.WriteEvent(Event{Time: s.Time, Code: Marker, Data: label}); err != nil {
			return err
		}
		line := prompt + strings.ReplaceAll(s.Command, "\n", "\r\n") + "\r\n"
		if err := enc.WriteEvent(Event{Time: s.Time, Code: Output, Data: line}); err != nil {
			return err
		}
	}
	return enc.Flush()
}
