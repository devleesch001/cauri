package ttystore

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestWriteAndOpen(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := "recorded output"
	sha := sum(content)
	for range 2 { // second write of the same file is harmless
		rel, err := s.Write(sha, int64(len(content)), strings.NewReader(content))
		if err != nil || rel != filepath.Join(sha[:2], sha) {
			t.Fatalf("Write = %q, %v", rel, err)
		}
	}
	f, err := s.Open(sha)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	got, _ := io.ReadAll(f)
	if string(got) != content {
		t.Fatalf("content = %q", got)
	}
}

func TestWriteMismatch(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	content := "recorded output"
	sha := sum(content)
	cases := map[string]struct {
		size int64
		body string
	}{
		"wrong content": {int64(len(content)), "recorded outpuX"},
		"too long":      {int64(len(content)) - 1, content},
		"too short":     {int64(len(content)) + 1, content},
	}
	for name, c := range cases {
		if _, err := s.Write(sha, c.size, strings.NewReader(c.body)); !errors.Is(err, ErrDigestMismatch) {
			t.Errorf("%s: err = %v, want ErrDigestMismatch", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, sha[:2], sha)); !os.IsNotExist(err) {
		t.Error("rejected upload left a file behind")
	}
	if left, _ := os.ReadDir(filepath.Join(dir, "tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestInvalidSHA(t *testing.T) {
	s, _ := New(t.TempDir())
	if _, err := s.Write("../../etc/passwd", 1, strings.NewReader("x")); err == nil {
		t.Error("want error for invalid sha")
	}
	if _, err := s.Open("../x"); err == nil {
		t.Error("want error for invalid sha")
	}
}
