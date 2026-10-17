package ttylog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

// Name returns the name Cowrie gives a recording: the SHA-256 of its
// input, all but the terminal output (Cowrie's ttylog_inputhash), the keys
// typed or the command of an exec channel. It tells which channel of a
// session a file is the recording of, and why Cowrie keeps one file per
// input.
func Name(r io.Reader) (string, error) {
	h := sha256.New()
	rd := NewReader(r)
	for {
		rec, err := rd.Next()
		if errors.Is(err, io.EOF) {
			return hex.EncodeToString(h.Sum(nil)), nil
		}
		if err != nil {
			return "", err
		}
		if rec.Op == OpWrite && rec.Dir != DirOutput {
			h.Write(rec.Data)
		}
	}
}
