// Package tail follows a log file like "tail -F", across rotations by
// renaming (cowrie.json -> cowrie.json.2026-10-14), and reports for every
// line a Position from which reading can resume after a restart.
//
// Files are identified by their inode and a fingerprint of their first
// bytes, never by name, so a position survives the rename of its file.
// The current file and its rotated siblings (same name plus a suffix) are
// ordered by modification time, the current file last.
package tail

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// fpMax is the number of leading bytes hashed to identify a file. Cowrie
// lines start with a timestamp, so one line is enough to tell files apart.
const fpMax = 512

// Compressed rotations cannot be followed and are ignored.
var compressedExt = []string{".gz", ".bz2", ".xz", ".zst", ".zip"}

// Position locates the end of a line in a file.
type Position struct {
	Path        string `json:"path"` // informational: the file may since have been renamed
	Inode       uint64 `json:"inode"`
	Fingerprint string `json:"fingerprint"` // hex SHA-256 of the first FPLen bytes
	FPLen       int64  `json:"fp_len"`
	Offset      int64  `json:"offset"`
}

// Line is a complete line, without its terminator, and the position just
// after it.
type Line struct {
	Data []byte
	Pos  Position
}

// Options configure a Follower.
type Options struct {
	Dir  string // directory of the log files
	Name string // name of the current file, such as "cowrie.json"
	// Poll is the delay between checks for new data at the end of the file.
	Poll time.Duration
	// FromStart, without saved position, starts at the oldest rotated file
	// instead of the end of the current file.
	FromStart bool
	// MaxLine bounds line length; longer lines are skipped.
	MaxLine int
	Logger  *slog.Logger
}

type file struct {
	f     *os.File
	path  string
	inode uint64
	mtime time.Time
	fp    string
	fpLen int64
}

// Follower reads the lines of the log files in order.
type Follower struct {
	opts     Options
	cur      *file
	readOff  int64  // offset in cur up to which data was read into buf
	buf      []byte // read but not yet returned data
	skipping bool   // discarding the rest of an over-long line
}

// Open returns a follower resuming at saved, or starting according to
// opts.FromStart if saved is nil. The files need not exist yet.
func Open(opts Options, saved *Position) (*Follower, error) {
	if opts.Poll <= 0 {
		opts.Poll = 500 * time.Millisecond
	}
	if opts.MaxLine <= 0 {
		opts.MaxLine = 1 << 20
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	t := &Follower{opts: opts}

	files, err := t.list()
	if err != nil {
		return nil, err
	}
	switch {
	case saved != nil:
		c := match(files, saved)
		if c == nil {
			opts.Logger.Warn("file of the saved position not found, reading every file again; the API drops duplicates",
				"path", saved.Path)
			if len(files) > 0 {
				return t, t.switchTo(files[0], 0)
			}
			return t, nil
		}
		off := saved.Offset
		if fi, err := os.Stat(c.path); err == nil && fi.Size() < off {
			opts.Logger.Warn("file shorter than the saved position, reading it from the start", "path", c.path)
			off = 0
		}
		return t, t.switchTo(c, off)
	case opts.FromStart:
		if len(files) > 0 {
			return t, t.switchTo(files[0], 0)
		}
	default:
		if n := len(files); n > 0 && files[n-1].path == t.currentPath() {
			fi, err := os.Stat(files[n-1].path)
			if err != nil {
				return nil, err
			}
			return t, t.switchTo(files[n-1], fi.Size())
		}
	}
	return t, nil
}

// Close releases the open file.
func (t *Follower) Close() error {
	if t.cur == nil {
		return nil
	}
	return t.cur.f.Close()
}

// Next returns the next complete line, waiting for one if needed.
func (t *Follower) Next(ctx context.Context) (Line, error) {
	for {
		if l, ok := t.lineFromBuf(); ok {
			return l, nil
		}
		n, err := t.readMore()
		if err != nil {
			return Line{}, err
		}
		if n > 0 {
			continue
		}
		moved, err := t.atEOF()
		if err != nil {
			return Line{}, err
		}
		if moved {
			continue
		}
		select {
		case <-ctx.Done():
			return Line{}, ctx.Err()
		case <-time.After(t.opts.Poll):
		}
	}
}

func (t *Follower) currentPath() string { return filepath.Join(t.opts.Dir, t.opts.Name) }

// lineFromBuf extracts the next complete line from the buffer.
func (t *Follower) lineFromBuf() (Line, bool) {
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			if len(t.buf) > t.opts.MaxLine {
				if !t.skipping {
					t.opts.Logger.Warn("skipping over-long line", "path", t.cur.path, "max", t.opts.MaxLine)
				}
				t.skipping = true
				t.buf = t.buf[:0]
			}
			return Line{}, false
		}
		data := t.buf[:i]
		t.buf = t.buf[i+1:]
		if t.skipping {
			t.skipping = false
			continue
		}
		if len(data) > t.opts.MaxLine {
			t.opts.Logger.Warn("skipping over-long line", "path", t.cur.path, "max", t.opts.MaxLine)
			continue
		}
		data = bytes.TrimRight(data, "\r")
		if len(data) == 0 {
			continue
		}
		end := t.readOff - int64(len(t.buf))
		return Line{Data: bytes.Clone(data), Pos: t.position(end)}, true
	}
}

func (t *Follower) position(offset int64) Position {
	c := t.cur
	if c.fpLen < fpMax && t.readOff > c.fpLen {
		if fp, n, err := fingerprint(c.f, t.readOff); err == nil {
			c.fp, c.fpLen = fp, n
		}
	}
	return Position{Path: c.path, Inode: c.inode, Fingerprint: c.fp, FPLen: c.fpLen, Offset: offset}
}

// readMore appends new data of the current file to the buffer, opening
// the first file if none is open yet.
func (t *Follower) readMore() (int, error) {
	if t.cur == nil {
		files, err := t.list()
		if err != nil || len(files) == 0 {
			return 0, err
		}
		if err := t.switchTo(files[0], 0); err != nil {
			return 0, err
		}
	}
	chunk := make([]byte, 64<<10)
	n, err := t.cur.f.ReadAt(chunk, t.readOff)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	t.buf = append(t.buf, chunk[:n]...)
	t.readOff += int64(n)
	return n, nil
}

// atEOF handles the end of the current file: moves to the next file if
// the current one was rotated, restarts it if it was truncated. It reports
// whether reading can go on immediately.
func (t *Follower) atEOF() (bool, error) {
	if t.cur == nil {
		return false, nil
	}
	// Fast path: still the current file, nothing rotated.
	if fi, err := os.Stat(t.currentPath()); err == nil && inodeOf(fi) == t.cur.inode && t.cur.inode != 0 {
		return t.checkTruncated()
	}
	files, err := t.list()
	if err != nil {
		return false, err
	}
	idx := -1
	for i, f := range files {
		if f.inode == t.cur.inode && t.sameStart(f) {
			idx = i
			break
		}
	}

	var next *file
	switch {
	case idx >= 0 && idx < len(files)-1:
		next = files[idx+1]
	case idx < 0:
		// Renamed to an ignored name or deleted: continue with the oldest
		// file not older than it.
		fi, err := t.cur.f.Stat()
		if err != nil {
			return false, err
		}
		for _, f := range files {
			if !f.mtime.Before(fi.ModTime()) {
				next = f
				break
			}
		}
	default:
		// Newest file: wait for data, unless it was truncated in place.
		return t.checkTruncated()
	}
	if next == nil {
		return false, nil
	}
	// The writer may have appended lines between our last read and the
	// rotation: drain the file before leaving it.
	if n, err := t.readMore(); err != nil || n > 0 {
		return n > 0, err
	}
	if len(t.buf) > 0 && !t.skipping {
		// A rotated file will not grow: its unterminated tail is a line.
		t.buf = append(t.buf, '\n')
		return true, nil
	}
	t.opts.Logger.Info("following next log file", "from", t.cur.path, "to", next.path)
	return true, t.switchTo(next, 0)
}

// checkTruncated restarts the current file if it shrank (copytruncate)
// and reports whether reading can go on.
func (t *Follower) checkTruncated() (bool, error) {
	fi, err := t.cur.f.Stat()
	if err != nil {
		return false, err
	}
	if fi.Size() < t.readOff {
		t.opts.Logger.Warn("file truncated, reading it from the start", "path", t.cur.path)
		t.readOff, t.buf, t.skipping = 0, t.buf[:0], false
		t.cur.fp, t.cur.fpLen = "", 0 // new content, new fingerprint
		return true, nil
	}
	return false, nil
}

// sameStart reports whether f starts with the bytes fingerprinted for cur.
func (t *Follower) sameStart(f *file) bool {
	if t.cur.fpLen == 0 {
		return true
	}
	fp, _, err := fingerprintPath(f.path, t.cur.fpLen)
	return err == nil && fp == t.cur.fp
}

func (t *Follower) switchTo(c *file, offset int64) error {
	f, err := os.Open(c.path)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if t.cur != nil {
		_ = t.cur.f.Close()
	}
	nc := &file{f: f, path: c.path, inode: inodeOf(fi), mtime: fi.ModTime()}
	nc.fp, nc.fpLen, err = fingerprint(f, fi.Size())
	if err != nil {
		_ = f.Close()
		return err
	}
	t.cur, t.readOff, t.buf, t.skipping = nc, offset, nil, false
	return nil
}

// list returns the current file and its rotations, oldest first.
func (t *Follower) list() ([]*file, error) {
	entries, err := os.ReadDir(t.opts.Dir)
	if err != nil {
		return nil, err
	}
	var files []*file
	for _, e := range entries {
		name := e.Name()
		// Regular files only: a symbolic link could lead out of the directory.
		if !e.Type().IsRegular() || (name != t.opts.Name && !strings.HasPrefix(name, t.opts.Name+".")) {
			continue
		}
		if ext := filepath.Ext(name); name != t.opts.Name && contains(compressedExt, ext) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue // removed meanwhile
		}
		files = append(files, &file{path: filepath.Join(t.opts.Dir, name), inode: inodeOf(fi), mtime: fi.ModTime()})
	}
	cur := t.currentPath()
	sort.SliceStable(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if (a.path == cur) != (b.path == cur) {
			return b.path == cur
		}
		if !a.mtime.Equal(b.mtime) {
			return a.mtime.Before(b.mtime)
		}
		return a.path < b.path
	})
	return files, nil
}

// match finds the file of a saved position: same inode and fingerprint,
// or else same fingerprint (inodes may change, for example on a copy).
func match(files []*file, p *Position) *file {
	var byFP *file
	for _, f := range files {
		ok := p.FPLen == 0
		if !ok {
			fp, _, err := fingerprintPath(f.path, p.FPLen)
			ok = err == nil && fp == p.Fingerprint
		}
		if !ok {
			continue
		}
		if f.inode == p.Inode {
			return f
		}
		if byFP == nil && p.FPLen > 0 {
			byFP = f
		}
	}
	return byFP
}

func fingerprint(f *os.File, size int64) (string, int64, error) {
	n := min(size, fpMax)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, 0); err != nil && !errors.Is(err, io.EOF) {
		return "", 0, err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), n, nil
}

// fingerprintPath hashes exactly the first n bytes of a file.
func fingerprintPath(path string, n int64) (string, int64, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path from the listing of the log directory
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	if _, err := io.ReadFull(f, buf); err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), n, nil
}

func inodeOf(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
