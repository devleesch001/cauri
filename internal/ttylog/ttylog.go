// Package ttylog reads the terminal recordings written by Cowrie and
// converts them to replayable events (package asciicast).
//
// A recording is a sequence of records, each a 24-byte little-endian
// header followed by its data:
//
//	op int32, tty uint32, length int32, direction int32, sec uint32, usec uint32
//
// Ops are open, write and close; a write carries terminal output or user
// input. The file comes from the honeypot: the reader trusts nothing in it.
package ttylog

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Record operations.
const (
	OpOpen  = 1
	OpClose = 2
	OpWrite = 3
)

// Write directions.
const (
	DirInput  = 1 // typed by the user
	DirOutput = 2 // shown on the terminal
)

// header is the layout of a record header (Cowrie's struct "<iLiiLL").
type header struct {
	Op     int32
	TTY    uint32
	Length int32
	Dir    int32
	Sec    uint32
	Usec   uint32
}

const headerSize = 24

// MaxData bounds the data of one record. Cowrie writes chunks of at most
// a few kilobytes; a larger length means a corrupt or hostile file.
const MaxData = 1 << 20

var (
	// ErrTruncated means the file ends in the middle of a record.
	ErrTruncated = errors.New("ttylog: truncated record")
	// ErrCorrupt means a record header holds impossible values.
	ErrCorrupt = errors.New("ttylog: corrupt record")
)

// Record is one entry of a recording.
type Record struct {
	Op   int32
	Dir  int32
	Time time.Time
	Data []byte
}

// Reader reads the records of a recording.
type Reader struct {
	r   *bufio.Reader
	hdr [headerSize]byte
}

// NewReader returns a reader of the recording in r.
func NewReader(r io.Reader) *Reader { return &Reader{r: bufio.NewReader(r)} }

// Next returns the next record, or io.EOF after the last one.
func (r *Reader) Next() (Record, error) {
	n, err := io.ReadFull(r.r, r.hdr[:])
	if err == io.EOF {
		return Record{}, io.EOF
	}
	if err != nil {
		return Record{}, fmt.Errorf("%w: header of %d bytes", ErrTruncated, n)
	}
	var h header
	if _, err := binary.Decode(r.hdr[:], binary.LittleEndian, &h); err != nil {
		return Record{}, err
	}
	if h.Length < 0 || h.Length > MaxData || h.Usec >= 1e6 {
		return Record{}, fmt.Errorf("%w: length %d, usec %d", ErrCorrupt, h.Length, h.Usec)
	}
	rec := Record{Op: h.Op, Dir: h.Dir, Time: time.Unix(int64(h.Sec), int64(h.Usec)*1000).UTC()}
	if h.Length > 0 {
		rec.Data = make([]byte, h.Length)
		if _, err := io.ReadFull(r.r, rec.Data); err != nil {
			return Record{}, fmt.Errorf("%w: data of %d bytes", ErrTruncated, h.Length)
		}
	}
	return rec, nil
}
