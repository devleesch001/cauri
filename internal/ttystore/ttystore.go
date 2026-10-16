// Package ttystore keeps raw Cowrie TTY recordings in a directory, named by
// their SHA-256 like Cowrie does. Files are never modified once written.
package ttystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/devleesch001/cauri/pkg/protocol"
)

// ErrDigestMismatch means the content does not match the announced digest
// or size.
var ErrDigestMismatch = errors.New("content does not match digest or size")

// Store is a directory of TTY recordings.
type Store struct {
	dir string
}

// New returns a store rooted at dir, creating it if needed.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("empty tty directory")
	}
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// RelPath returns the path of a recording relative to the store root.
func RelPath(sha string) string { return filepath.Join(sha[:2], sha) }

// Write stores r, which must hold size bytes hashing to sha (lower-case
// hex), and returns its relative path. Writing an existing file is a no-op.
func (s *Store) Write(sha string, size int64, r io.Reader) (string, error) {
	if !protocol.ValidSHA256(sha) {
		return "", fmt.Errorf("invalid sha256 %q", sha)
	}
	rel := RelPath(sha)
	final := filepath.Join(s.dir, rel)

	tmp, err := os.CreateTemp(filepath.Join(s.dir, "tmp"), "upload-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()

	h := sha256.New()
	// Read one byte more than announced to detect oversized bodies.
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, size+1))
	if err != nil {
		return "", err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != sha {
		return "", ErrDigestMismatch
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		return "", err
	}
	return rel, nil
}

// Open opens a stored recording.
func (s *Store) Open(sha string) (*os.File, error) {
	if !protocol.ValidSHA256(sha) {
		return nil, fmt.Errorf("invalid sha256 %q", sha)
	}
	return os.Open(filepath.Join(s.dir, RelPath(sha)))
}

// Remove deletes a stored recording. A recording already gone is not an
// error: removing twice changes nothing.
func (s *Store) Remove(sha string) error {
	if !protocol.ValidSHA256(sha) {
		return fmt.Errorf("invalid sha256 %q", sha)
	}
	err := os.Remove(filepath.Join(s.dir, RelPath(sha)))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Available tells whether the volume is there: a recording missing from
// an unavailable volume (not mounted, say) is not a removed one.
func (s *Store) Available() bool {
	fi, err := os.Stat(filepath.Join(s.dir, "tmp"))
	return err == nil && fi.IsDir()
}
