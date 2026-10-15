package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/asciicast"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttylog"
)

// Replays keep the real timing; idle_time_limit only asks the player to
// shorten long pauses, and can be changed or disabled (0) by the client.
const (
	defaultIdleTimeLimit = 2 * time.Second
	maxIdleTimeLimit     = time.Hour
)

// castVersion changes when the conversion of a same file changes, so that
// clients do not keep an old replay (it is part of the ETag).
const castVersion = "v3.3"

// getCast converts the raw recording of a session to asciicast v3 on the
// fly. The stored file is never modified.
func (s *server) getCast(w http.ResponseWriter, r *http.Request) {
	idle, err := idleTimeLimit(r)
	if err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, err.Error()))
		return
	}
	rec, err := store.GetRecording(r.Context(), s.DB, sessionID(r))
	switch {
	case errors.Is(err, store.ErrNotFound):
		render.Render(w, r, errorOf(http.StatusNotFound, "session not found"))
		return
	case errors.Is(err, store.ErrNoRecording):
		render.Render(w, r, errorOf(http.StatusNotFound, "this session has no stored recording"))
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	// Files never change: the ETag depends only on them, where they are
	// placed, and how they are converted.
	tag := recordingTag(rec)
	etag := fmt.Sprintf(`"%s-%s-%d"`, tag, castVersion, idle.Milliseconds())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	recording, closeAll, ok := s.openRecording(w, r, rec)
	if !ok {
		return
	}
	defer closeAll()

	sizes := make([]ttylog.TermSize, len(rec.Sizes))
	for i, sz := range rec.Sizes {
		sizes[i] = ttylog.TermSize{Time: sz.TS.Time, Cols: sz.Cols, Rows: sz.Rows}
	}
	w.Header().Set("Content-Type", asciicast.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.cast"`, rec.Session))
	out := &countingWriter{w: w}
	err = ttylog.Convert(recording, asciicast.NewV3(out), ttylog.Options{
		Sizes: sizes, IdleTimeLimit: idle, Commands: commands(rec),
	})
	switch {
	case err != nil && out.n == 0:
		w.Header().Del("Content-Disposition")
		w.Header().Del("ETag")
		s.Logger.Error("unreadable recording", "session", rec.Session, "err", err)
		render.Render(w, r, errorOf(http.StatusUnprocessableEntity, "the recording cannot be read"))
	case err != nil:
		// Already streaming: the replay ends at the damage.
		s.Logger.Warn("damaged recording, replay truncated", "session", rec.Session, "err", err)
	}
}

// chaptersVersion changes when the extraction of chapters changes.
const chaptersVersion = "c2"

// chapter is a command of a replay and what it printed, see ttylog.Chapter.
type chapter struct {
	Time       float64 `json:"time"` // seconds from the start of the replay
	Command    string  `json:"command"`
	Output     string  `json:"output"`
	Truncated  bool    `json:"truncated"`
	FullScreen bool    `json:"full_screen"`
}

type chapters struct {
	Records  int       `json:"records"` // records in the recording
	Chapters []chapter `json:"chapters"`
}

// getChapters returns the commands of a session's recording with their
// output as plain text, for the chapter list next to the replay.
func (s *server) getChapters(w http.ResponseWriter, r *http.Request) {
	rec, err := store.GetRecording(r.Context(), s.DB, sessionID(r))
	switch {
	case errors.Is(err, store.ErrNotFound):
		render.Render(w, r, errorOf(http.StatusNotFound, "session not found"))
		return
	case errors.Is(err, store.ErrNoRecording):
		render.Render(w, r, errorOf(http.StatusNotFound, "this session has no stored recording"))
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	etag := fmt.Sprintf(`"%s-%s-%d"`, recordingTag(rec), chaptersVersion, len(rec.Commands))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	recording, closeAll, ok := s.openRecording(w, r, rec)
	if !ok {
		return
	}
	defer closeAll()
	sum, err := ttylog.Chapters(recording, commands(rec))
	if err != nil {
		w.Header().Del("ETag")
		s.Logger.Error("unreadable recording", "session", rec.Session, "err", err)
		render.Render(w, r, errorOf(http.StatusUnprocessableEntity, "the recording cannot be read"))
		return
	}
	out := chapters{Records: sum.Records, Chapters: make([]chapter, len(sum.Chapters))}
	for i, c := range sum.Chapters {
		out.Chapters[i] = chapter{
			Time: c.Time.Seconds(), Command: c.Command, Output: c.Output,
			Truncated: c.Truncated, FullScreen: c.FullScreen,
		}
	}
	render.JSON(w, r, out)
}

// openRecording opens the stored files of a session's recordings as one
// recording. Files missing from the volume are logged and left out; with
// none left, it answers 500 and returns false.
func (s *server) openRecording(w http.ResponseWriter, r *http.Request, rec *store.Recording) (io.ReadSeeker, func(), bool) {
	parts, missing, closeAll := s.Ingest.OpenRecording(rec.Files)
	if len(missing) > 0 {
		s.Logger.Error("recordings missing from the volume", "session", rec.Session, "sha256", missing)
	}
	if len(parts) == 0 {
		closeAll()
		w.Header().Del("ETag")
		s.internalError(w, r, fmt.Errorf("session %s: no recording file in the volume", rec.Session))
		return nil, nil, false
	}
	return ttylog.Join(parts...), closeAll, true
}

// recordingTag identifies the files of a replay and where they are placed.
func recordingTag(rec *store.Recording) string {
	h := sha256.New()
	for _, f := range rec.Files {
		_, _ = fmt.Fprintf(h, "%s@", f.SHA256)
		if f.At != nil {
			_, _ = fmt.Fprint(h, f.At.UnixMicro())
		}
		_, _ = fmt.Fprint(h, ";")
	}
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// commands returns the commands of a recording as replay chapters.
func commands(rec *store.Recording) []ttylog.Command {
	cmds := make([]ttylog.Command, len(rec.Commands))
	for i, c := range rec.Commands {
		cmds[i] = ttylog.Command{Time: c.TS.Time, Input: c.Input}
	}
	return cmds
}

// idleTimeLimit reads the idle_time_limit parameter, in seconds.
func idleTimeLimit(r *http.Request) (time.Duration, error) {
	v := r.URL.Query().Get("idle_time_limit")
	if v == "" {
		return defaultIdleTimeLimit, nil
	}
	sec, err := strconv.ParseFloat(v, 64)
	if err != nil || sec < 0 || sec > maxIdleTimeLimit.Seconds() {
		return 0, fmt.Errorf("parameter idle_time_limit: want seconds between 0 (real time) and %d", int(maxIdleTimeLimit.Seconds()))
	}
	return time.Duration(sec * float64(time.Second)), nil
}

// countingWriter tells whether anything was sent, after which the status
// can no longer change.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
