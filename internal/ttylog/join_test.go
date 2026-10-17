package ttylog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	// A channel open alongside the first one: its records go between.
	beside := file(rec(OpWrite, DirOutput, 500*time.Millisecond, "x"), rec(OpWrite, DirOutput, 6*time.Second, "y"))

	j := Join(
		Part{R: bytes.NewReader(a)},
		Part{R: bytes.NewReader(b)},
		Part{R: bytes.NewReader(dup), At: t0.Add(10 * time.Second)},
		Part{R: bytes.NewReader(beside)},
	)
	at, data, err := readAll(t, j)
	want := []time.Duration{0, 500 * time.Millisecond, time.Second, 5 * time.Second, 6 * time.Second, 10 * time.Second, 10*time.Second + 500*time.Millisecond}
	if err != nil || !slices.Equal(at, want) || !slices.Equal(data, []string{"", "x", "a", "b", "y", "", "d"}) {
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

// A damaged part keeps its records read so far, the others go on, and the
// damage is reported at the end.
func TestJoinDamaged(t *testing.T) {
	good := file(rec(OpWrite, DirOutput, 2*time.Second, "g"))
	bad := append(file(rec(OpWrite, DirOutput, time.Second, "b")), 1, 2, 3)
	at, data, err := readAll(t, Join(Part{R: bytes.NewReader(bad)}, Part{R: bytes.NewReader(good)}))
	if !errors.Is(err, ErrTruncated) || !slices.Equal(data, []string{"b", "g"}) || len(at) != 2 {
		t.Fatalf("joined = %v %q, %v", at, data, err)
	}
}

// Cowrie names a recording by its input: Name finds the name of each real
// file, an exec channel's as a shell's.
func TestName(t *testing.T) {
	for _, dir := range []string{"../ingest/testdata/cowrie-v3.1.2-exec-tty"} {
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			data, err := os.ReadFile(filepath.Join(dir, f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if got, err := Name(bytes.NewReader(data)); err != nil || got != f.Name() {
				t.Errorf("%s: name %s, %v", f.Name(), got, err)
			}
		}
	}
	if _, err := Name(bytes.NewReader([]byte{1, 2})); !errors.Is(err, ErrTruncated) {
		t.Errorf("damaged: %v", err)
	}
}

func TestEncode(t *testing.T) {
	recs := []Record{
		{Op: OpWrite, Dir: DirOutput, Time: t0.Add(time.Second), Data: []byte("a")},
		{Op: OpWrite, Dir: DirOutput, Time: t0.Add(1500 * time.Millisecond), Data: []byte("b")},
	}
	at, data, err := readAll(t, bytes.NewReader(Encode(recs...)))
	if err != nil || !slices.Equal(at, []time.Duration{time.Second, 1500 * time.Millisecond}) || !slices.Equal(data, []string{"a", "b"}) {
		t.Errorf("encoded = %v %q, %v", at, data, err)
	}
}
