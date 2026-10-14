package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

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
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rec, err := store.GetRecording(r.Context(), s.DB, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "session not found")
		return
	case errors.Is(err, store.ErrNoRecording):
		writeError(w, http.StatusNotFound, "this session has no stored recording")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}

	// The file never changes: the ETag depends only on it and on how it
	// is converted.
	etag := fmt.Sprintf(`"%s-%s-%d"`, rec.SHA256, castVersion, idle.Milliseconds())
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	f, err := s.TTY.Open(rec.SHA256)
	if err != nil {
		s.internalError(w, r, fmt.Errorf("recording %s: %w", rec.SHA256, err))
		return
	}
	defer func() { _ = f.Close() }()

	sizes := make([]ttylog.TermSize, len(rec.Sizes))
	for i, sz := range rec.Sizes {
		sizes[i] = ttylog.TermSize{Time: sz.TS, Cols: sz.Cols, Rows: sz.Rows}
	}
	w.Header().Set("Content-Type", asciicast.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.cast"`, rec.Session))
	out := &countingWriter{w: w}
	err = ttylog.Convert(f, asciicast.NewV3(out), ttylog.Options{
		Sizes: sizes, IdleTimeLimit: idle, Commands: commands(rec),
	})
	switch {
	case err != nil && out.n == 0:
		w.Header().Del("Content-Disposition")
		w.Header().Del("ETag")
		s.Logger.Error("unreadable recording", "sha256", rec.SHA256, "err", err)
		writeError(w, http.StatusUnprocessableEntity, "the recording cannot be read")
	case err != nil:
		// Already streaming: the replay ends at the damage.
		s.Logger.Warn("damaged recording, replay truncated", "sha256", rec.SHA256, "err", err)
	}
}

// chaptersVersion changes when the extraction of chapters changes.
const chaptersVersion = "c1"

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
	rec, err := store.GetRecording(r.Context(), s.DB, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "session not found")
		return
	case errors.Is(err, store.ErrNoRecording):
		writeError(w, http.StatusNotFound, "this session has no stored recording")
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	etag := fmt.Sprintf(`"%s-%s-%d"`, rec.SHA256, chaptersVersion, len(rec.Commands))
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	f, err := s.TTY.Open(rec.SHA256)
	if err != nil {
		s.internalError(w, r, fmt.Errorf("recording %s: %w", rec.SHA256, err))
		return
	}
	defer func() { _ = f.Close() }()
	sum, err := ttylog.Chapters(f, commands(rec))
	if err != nil {
		w.Header().Del("ETag")
		s.Logger.Error("unreadable recording", "sha256", rec.SHA256, "err", err)
		writeError(w, http.StatusUnprocessableEntity, "the recording cannot be read")
		return
	}
	out := chapters{Records: sum.Records, Chapters: make([]chapter, len(sum.Chapters))}
	for i, c := range sum.Chapters {
		out.Chapters[i] = chapter{
			Time: c.Time.Seconds(), Command: c.Command, Output: c.Output,
			Truncated: c.Truncated, FullScreen: c.FullScreen,
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// commands returns the commands of a recording as replay chapters.
func commands(rec *store.Recording) []ttylog.Command {
	cmds := make([]ttylog.Command, len(rec.Commands))
	for i, c := range rec.Commands {
		cmds[i] = ttylog.Command{Time: c.TS, Input: c.Input}
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
