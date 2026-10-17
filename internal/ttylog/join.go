package ttylog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"time"
)

// Part is the recording of one SSH channel of a session, to read with the
// others (Join).
type Part struct {
	R io.ReadSeeker
	// At, if set, moves the recording so that it starts then. Cowrie keeps
	// one file per input: a recording it marks duplicate is the file of an
	// earlier one, with that one's times, maybe of another session. Unset,
	// the times of the file are kept.
	At time.Time
}

// Join returns the recordings of a session's channels as one, as a
// terminal showing them all would have recorded it: their records in time
// order, a record of an earlier part first at equal times. Channels may
// overlap (a shell and an exec channel at once); bots open them one after
// the other, and their records then follow part after part. A part that
// cannot be read ends there, its records read so far kept: the others go
// on, and the error is returned once they are read. Seeking is only
// supported back to the start, which reads the parts again.
func Join(parts ...Part) io.ReadSeeker { return &joined{parts: parts} }

type joined struct {
	parts   []Part
	heads   []head
	started bool
	buf     bytes.Buffer // encoded records not read yet
	damage  error        // of a part, returned at the end
	err     error
}

// head is the next record of a part.
type head struct {
	rd    *Reader
	shift time.Duration
	rec   Record
	ok    bool // rec is set; false once the part is read
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

func (j *joined) start() {
	j.started = true
	j.heads = make([]head, len(j.parts))
	for i, part := range j.parts {
		if _, err := part.R.Seek(0, io.SeekStart); err != nil {
			j.damage = cmpErr(j.damage, err)
			continue
		}
		h := &j.heads[i]
		h.rd = NewReader(part.R)
		j.advance(h)
		if h.ok && !part.At.IsZero() {
			h.shift = part.At.Sub(h.rec.Time)
			h.rec.Time = part.At
		}
	}
}

// advance reads the next record of a part, shifted.
func (j *joined) advance(h *head) {
	rec, err := h.rd.Next()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			j.damage = cmpErr(j.damage, err)
		}
		h.ok = false
		return
	}
	rec.Time = rec.Time.Add(h.shift)
	h.rec, h.ok = rec, true
}

// fill encodes the earliest next record, or sets err at the end.
func (j *joined) fill() {
	if !j.started {
		j.start()
	}
	next := -1
	for i := range j.heads {
		if j.heads[i].ok && (next < 0 || j.heads[i].rec.Time.Before(j.heads[next].rec.Time)) {
			next = i
		}
	}
	if next < 0 {
		j.err = io.EOF
		if j.damage != nil {
			j.err = j.damage
		}
		return
	}
	h := &j.heads[next]
	encode(&j.buf, h.rec, h.rec.Time)
	j.advance(h)
}

func (j *joined) Seek(offset int64, whence int) (int64, error) {
	if offset != 0 || whence != io.SeekStart {
		return 0, errors.New("ttylog: joined recordings only seek back to the start")
	}
	j.started, j.heads, j.damage, j.err = false, nil, nil, nil
	j.buf.Reset()
	return 0, nil
}

func cmpErr(old, err error) error {
	if old != nil {
		return old
	}
	return err
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
