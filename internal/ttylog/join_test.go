package ttylog

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"
	"time"
)

// readAll returns the records of r with their offsets from t0.
func readAll(t *testing.T, r io.Reader) ([]time.Duration, []string, error) {
	t.Helper()
	var at []time.Duration
	var data []string
	rd := NewReader(r)
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return at, data, nil
		}
		if err != nil {
			return at, data, err
		}
		at = append(at, rec.Time.Sub(t0))
		data = append(data, string(rec.Data))
	}
}

func TestJoin(t *testing.T) {
	a := file(rec(OpOpen, 0, 0, ""), rec(OpWrite, DirOutput, time.Second, "a"))
	b := file(rec(OpWrite, DirOutput, 5*time.Second, "b"))
	// A duplicate: Cowrie's file of an earlier session, 100 hours before.
	dup := file(rec(OpOpen, 0, -100*time.Hour, ""), rec(OpWrite, DirOutput, -100*time.Hour+500*time.Millisecond, "d"))

	j := Join(
		Part{R: bytes.NewReader(a)},
		Part{R: bytes.NewReader(b)},
		Part{R: bytes.NewReader(dup), At: t0.Add(10 * time.Second)},
	)
	at, data, err := readAll(t, j)
	want := []time.Duration{0, time.Second, 5 * time.Second, 10 * time.Second, 10*time.Second + 500*time.Millisecond}
	if err != nil || !slices.Equal(at, want) || !slices.Equal(data, []string{"", "a", "b", "", "d"}) {
		t.Fatalf("joined = %v %q, %v", at, data, err)
	}

	// Chapters reads twice: back at the start, the same records.
	if _, err := j.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	again, _, err := readAll(t, j)
	if err != nil || !slices.Equal(again, want) {
		t.Errorf("read again = %v, %v", again, err)
	}
	if _, err := j.Seek(5, io.SeekStart); err == nil {
		t.Error("seek elsewhere than the start accepted")
	}
}

// Channels may overlap: a record never goes back before the previous one.
func TestJoinTimeGoesForward(t *testing.T) {
	a := file(rec(OpWrite, DirOutput, 3*time.Second, "a"))
	b := file(rec(OpWrite, DirOutput, time.Second, "b"), rec(OpWrite, DirOutput, 4*time.Second, "c"))
	at, _, err := readAll(t, Join(Part{R: bytes.NewReader(a)}, Part{R: bytes.NewReader(b)}))
	if err != nil || !slices.Equal(at, []time.Duration{3 * time.Second, 3 * time.Second, 4 * time.Second}) {
		t.Errorf("times = %v, %v", at, err)
	}
}

// A damaged part ends the recording after what could be read.
func TestJoinDamaged(t *testing.T) {
	a := file(rec(OpWrite, DirOutput, 0, "a"))
	broken := rec(OpWrite, DirOutput, time.Second, "lost")[:headerSize+2]
	at, data, err := readAll(t, Join(Part{R: bytes.NewReader(a)}, Part{R: bytes.NewReader(broken)}))
	if err == nil || len(at) != 1 || data[0] != "a" {
		t.Errorf("damaged = %v %q, %v", at, data, err)
	}
	if _, _, err := readAll(t, Join()); err != nil {
		t.Errorf("no parts: %v", err)
	}
}

// The joined recording converts like one Cowrie file, with one marker per
// command, the duplicate at its own time.
func TestConvertJoined(t *testing.T) {
	a := file(rec(OpOpen, 0, 0, ""), rec(OpWrite, DirOutput, 10*time.Millisecond, "Linux\n"))
	dup := file(rec(OpOpen, 0, -time.Hour, ""), rec(OpWrite, DirOutput, -time.Hour+5*time.Millisecond, "root\n"))
	joined, err := io.ReadAll(Join(Part{R: bytes.NewReader(a)}, Part{R: bytes.NewReader(dup), At: t0.Add(2 * time.Second)}))
	if err != nil {
		t.Fatal(err)
	}
	_, events, err := convert(t, joined, Options{Commands: []Command{
		{Time: t0.Add(time.Millisecond), Input: "uname"}, {Time: t0.Add(2*time.Second + time.Millisecond), Input: "whoami"},
	}})
	want := []event{
		{time.Millisecond, "m", "uname"}, {10 * time.Millisecond, "o", "Linux\r\n"},
		{2*time.Second + time.Millisecond, "m", "whoami"}, {2*time.Second + 5*time.Millisecond, "o", "root\r\n"},
	}
	if err != nil || !slices.Equal(events, want) {
		t.Errorf("events = %+v, %v", events, err)
	}
}
