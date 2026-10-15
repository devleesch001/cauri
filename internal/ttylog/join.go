package ttylog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

// Part is one recording of a session, to read with others (Join).
type Part struct {
	R io.ReadSeeker
	// At, if set, moves the recording so that it starts then. Cowrie keeps
	// one file per distinct output: a recording it marks duplicate is the
	// file of an earlier one, with that one's times, maybe of another
	// session. Unset, the times of the file are kept.
	At time.Time
}

// Join returns recordings one after the other as a single recording, as
// Cowrie would have written it, for the readers of this package and of
// package verdict: a session runs a recording per SSH channel, one per
// command for many bots. A record never goes back before the previous one,
// so that time only moves forward. A part that cannot be read ends the
// recording there, with its error. Seeking is only supported back to the
// start, which reads the parts again (Chapters reads twice).
func Join(parts ...Part) io.ReadSeeker { return &joined{parts: parts} }

type joined struct {
	parts []Part
	i     int // part being read
	rd    *Reader
	shift time.Duration // of the part being read
	last  time.Time
	buf   bytes.Buffer // encoded records not read yet
	err   error
}

func (j *joined) Read(p []byte) (int, error) {
	for j.buf.Len() == 0 {
		if j.err != nil {
			return 0, j.err
		}
		j.fill()
	}
	return j.buf.Read(p)
}

// fill encodes the next record, or sets err at the end or on damage.
func (j *joined) fill() {
	for j.i < len(j.parts) {
		part := j.parts[j.i]
		first := j.rd == nil
		if first {
			if _, err := part.R.Seek(0, io.SeekStart); err != nil {
				j.err = err
				return
			}
			j.rd, j.shift = NewReader(part.R), 0
		}
		rec, err := j.rd.Next()
		if errors.Is(err, io.EOF) {
			j.i, j.rd = j.i+1, nil
			continue
		}
		if err != nil {
			j.err = err
			return
		}
		if first && !part.At.IsZero() {
			j.shift = part.At.Sub(rec.Time)
		}
		t := rec.Time.Add(j.shift)
		if t.Before(j.last) {
			t = j.last
		}
		j.last = t
		encode(&j.buf, rec, t)
		return
	}
	j.err = io.EOF
}

func (j *joined) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errors.New("ttylog: joined recordings only seek back to the start")
	}
	j.i, j.rd, j.shift, j.last, j.err = 0, nil, 0, time.Time{}, nil
	j.buf.Reset()
	return 0, nil
}

// encode writes rec at time t in Cowrie's format. Times outside its range
// (a shift cannot produce them from valid files) are clamped.
func encode(b *bytes.Buffer, rec Record, t time.Time) {
	sec := min(max(t.Unix(), 0), math.MaxUint32)
	h := header{
		Op: rec.Op, Dir: rec.Dir,
		Length: int32(len(rec.Data)),          //nolint:gosec // G115: at most MaxData, checked by the reader
		Sec:    uint32(sec),                   //nolint:gosec // G115: clamped above
		Usec:   uint32(t.Nanosecond() / 1000), //nolint:gosec // G115: under 1e6
	}
	_ = binary.Write(b, binary.LittleEndian, h) // a bytes.Buffer does not fail
	b.Write(rec.Data)
}
