package tail

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const name = "cowrie.json"

func opts(dir string) Options {
	return Options{
		Dir: dir, Name: name, Poll: 5 * time.Millisecond, FromStart: true, MaxLine: 64,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func appendTo(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
}

// ev returns a distinct line, with a timestamp-like prefix as Cowrie writes.
func ev(i int) string { return fmt.Sprintf(`{"timestamp":"2026-10-14T09:00:%02d","n":%d}`+"\n", i%60, i) }

func next(t *testing.T, f *Follower) Line {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	l, err := f.Next(ctx)
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return l
}

func expect(t *testing.T, f *Follower, from, to int) Line {
	t.Helper()
	var l Line
	for i := from; i <= to; i++ {
		l = next(t, f)
		if want := strings.TrimSuffix(ev(i), "\n"); string(l.Data) != want {
			t.Fatalf("line = %s, want %s", l.Data, want)
		}
	}
	return l
}

func expectNothing(t *testing.T, f *Follower) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if l, err := f.Next(ctx); err == nil {
		t.Fatalf("unexpected line %s", l.Data)
	}
}

func open(t *testing.T, o Options, saved *Position) *Follower {
	t.Helper()
	f, err := Open(o, saved)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestFollowAndPartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	f := open(t, opts(dir), nil) // file does not exist yet
	appendTo(t, path, ev(1), ev(2))
	l := expect(t, f, 1, 2)
	if l.Pos.Offset != int64(len(ev(1)+ev(2))) || l.Pos.Fingerprint == "" {
		t.Errorf("position = %+v", l.Pos)
	}

	half := ev(3)[:10]
	appendTo(t, path, half)
	expectNothing(t, f)
	appendTo(t, path, ev(3)[10:])
	expect(t, f, 3, 3)
}

// Cowrie's daily rotation: rename, then create a new file. Lines written
// to the old file after the rename must not be lost.
func TestRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path, ev(1), ev(2))
	f := open(t, opts(dir), nil)
	expect(t, f, 1, 2)

	old, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".2026-10-14"); err != nil {
		t.Fatal(err)
	}
	_, _ = old.WriteString(ev(3)) // writer still holds the old file
	_ = old.Close()
	time.Sleep(10 * time.Millisecond) // new file strictly newer
	appendTo(t, path, ev(4), ev(5))

	expect(t, f, 3, 5)
	appendTo(t, path, ev(6))
	expect(t, f, 6, 6)
}

func TestResume(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path, ev(1), ev(2), ev(3))
	f := open(t, opts(dir), nil)
	pos := expect(t, f, 1, 2).Pos
	_ = f.Close()

	f = open(t, opts(dir), &pos)
	expect(t, f, 3, 3)
	expectNothing(t, f)
}

// A position saved in a file that was rotated while the ingestor was down.
func TestResumeAfterRotation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path, ev(1), ev(2), ev(3))
	f := open(t, opts(dir), nil)
	pos := expect(t, f, 1, 1).Pos
	_ = f.Close()

	if err := os.Rename(path, path+".2026-10-14"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	appendTo(t, path, ev(4))

	f = open(t, opts(dir), &pos)
	expect(t, f, 2, 4)
}

// Without inode match (file copied), the fingerprint still finds the file.
func TestResumeByFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path, ev(1), ev(2))
	f := open(t, opts(dir), nil)
	pos := expect(t, f, 1, 1).Pos
	_ = f.Close()
	pos.Inode = 0

	f = open(t, opts(dir), &pos)
	expect(t, f, 2, 2)
}

func TestSavedFileGone(t *testing.T) {
	dir := t.TempDir()
	appendTo(t, filepath.Join(dir, name), ev(1))
	pos := Position{Inode: 1, Fingerprint: "00", FPLen: 10, Offset: 5}
	f := open(t, opts(dir), &pos)
	expect(t, f, 1, 1) // everything read again
}

func TestFromStartOrdersRotations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	base := time.Now().Add(-time.Hour)
	// Names sort differently from dates: order must come from mtimes.
	files := []struct {
		suffix string
		lines  []string
		age    time.Duration
	}{
		{".b", []string{ev(1), ev(2)}, 0},
		{".a", []string{ev(3)}, time.Minute},
		{".old.gz", []string{"ignored\n"}, 2 * time.Minute},
	}
	for _, c := range files {
		appendTo(t, path+c.suffix, c.lines...)
		if err := os.Chtimes(path+c.suffix, base.Add(c.age), base.Add(c.age)); err != nil {
			t.Fatal(err)
		}
	}
	appendTo(t, path, ev(4))
	if err := os.Chtimes(path, base, base); err != nil { // even if older, current is last
		t.Fatal(err)
	}

	f := open(t, opts(dir), nil)
	expect(t, f, 1, 4)
	expectNothing(t, f)
}

func TestStartAtEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path+".2026-10-13", ev(1))
	appendTo(t, path, ev(2))
	o := opts(dir)
	o.FromStart = false
	f := open(t, o, nil)
	appendTo(t, path, ev(3))
	expect(t, f, 3, 3)
}

func TestTruncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	appendTo(t, path, ev(1), ev(2))
	f := open(t, opts(dir), nil)
	expect(t, f, 1, 2)
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, ev(3))
	l := expect(t, f, 3, 3)
	if l.Pos.Offset != int64(len(ev(3))) {
		t.Errorf("offset = %d after truncation", l.Pos.Offset)
	}
}

func TestOverlongLine(t *testing.T) {
	dir := t.TempDir()
	appendTo(t, filepath.Join(dir, name), ev(1), strings.Repeat("x", 200)+"\n", ev(2))
	f := open(t, opts(dir), nil) // MaxLine 64
	expect(t, f, 1, 2)
}
