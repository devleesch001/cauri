package api

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
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

// replaySource returns the stored recording of a session and its file, or
// neither when the replay is simulated: the session has no recording (a
// bot opening a channel per command sends none), or its file was removed
// from the volume, which is how an operator limits retention.
func (s *server) replaySource(r *http.Request) (*store.Recording, *os.File, error) {
	rec, err := store.GetRecording(r.Context(), s.DB, sessionID(r))
	if errors.Is(err, store.ErrNoRecording) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	f, err := s.TTY.Open(rec.SHA256)
	if errors.Is(err, fs.ErrNotExist) {
		s.Logger.Info("recording file missing, replay simulated", "sha256", rec.SHA256)
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("recording %s: %w", rec.SHA256, err)
	}
	return rec, f, nil
}

// simulatedSteps returns the commands of a session as steps of a simulated
// replay, from the first command, and the prompt to show.
func (s *server) simulatedSteps(r *http.Request) (start time.Time, prompt string, steps []asciicast.Step, err error) {
	sim, err := store.GetSimulation(r.Context(), s.DB, sessionID(r))
	if err != nil {
		return start, "", nil, err
	}
	if len(sim.Commands) == 0 {
		return start, "", nil, errNothingToReplay
	}
	start = sim.Commands[0].TS.Time
	prompt = "$ "
	if sim.User == "root" {
		prompt = "# "
	}
	for _, c := range sim.Commands {
		steps = append(steps, asciicast.Step{Time: max(c.TS.Sub(start), 0), Command: c.Input})
	}
	return start, prompt, steps, nil
}

var errNothingToReplay = errors.New("nothing to replay")

// replayError answers the errors of the replay endpoints.
func (s *server) replayError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		render.Render(w, r, errorOf(http.StatusNotFound, "session not found"))
	case errors.Is(err, errNothingToReplay):
		render.Render(w, r, errorOf(http.StatusNotFound, "this session has no recording and no command to replay"))
	default:
		s.internalError(w, r, err)
	}
}

// getCast converts the raw recording of a session to asciicast v3 on the
// fly. The stored file is never modified. Without a recording, the replay
// is simulated from the commands: their timing, without any output.
func (s *server) getCast(w http.ResponseWriter, r *http.Request) {
	idle, err := idleTimeLimit(r)
	if err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, err.Error()))
		return
	}
	rec, f, err := s.replaySource(r)
	if err != nil {
		s.replayError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	if f == nil {
		s.simulatedCast(w, r, idle)
		return
	}
	defer func() { _ = f.Close() }()

	// The file never changes: the ETag depends only on it and on how it
	// is converted.
	etag := fmt.Sprintf(`"%s-%s-%d"`, rec.SHA256, castVersion, idle.Milliseconds())
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	sizes := make([]ttylog.TermSize, len(rec.Sizes))
	for i, sz := range rec.Sizes {
		sizes[i] = ttylog.TermSize{Time: sz.TS.Time, Cols: sz.Cols, Rows: sz.Rows}
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
		render.Render(w, r, errorOf(http.StatusUnprocessableEntity, "the recording cannot be read"))
	case err != nil:
		// Already streaming: the replay ends at the damage.
		s.Logger.Warn("damaged recording, replay truncated", "sha256", rec.SHA256, "err", err)
	}
}

// simulatedCast writes the replay of a session without recording. It
// changes while the session runs, so it has no ETag.
func (s *server) simulatedCast(w http.ResponseWriter, r *http.Request, idle time.Duration) {
	start, prompt, steps, err := s.simulatedSteps(r)
	if err != nil {
		s.replayError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", asciicast.ContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%d-simulated.cast"`, sessionID(r)))
	h := asciicast.Header{Cols: ttylog.DefaultCols, Rows: ttylog.DefaultRows, Timestamp: start, IdleTimeLimit: idle}
	if err := asciicast.Simulate(asciicast.NewV3(w), h, prompt, steps); err != nil {
		s.Logger.Warn("simulated replay interrupted", "err", err)
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
	Records int `json:"records"` // records in the recording
	// Simulated: no recording, the chapters are the commands without output.
	Simulated bool      `json:"simulated"`
	Chapters  []chapter `json:"chapters"`
}

// getChapters returns the commands of a session's recording with their
// output as plain text, for the chapter list next to the replay. Without a
// recording they come without output, as in the simulated replay.
func (s *server) getChapters(w http.ResponseWriter, r *http.Request) {
	rec, f, err := s.replaySource(r)
	if err != nil {
		s.replayError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	if f == nil {
		_, _, steps, err := s.simulatedSteps(r)
		if err != nil {
			s.replayError(w, r, err)
			return
		}
		out := chapters{Simulated: true, Chapters: make([]chapter, len(steps))}
		for i, st := range steps {
			out.Chapters[i] = chapter{Time: st.Time.Seconds(), Command: st.Command}
		}
		render.JSON(w, r, out)
		return
	}
	defer func() { _ = f.Close() }()
	etag := fmt.Sprintf(`"%s-%s-%d"`, rec.SHA256, chaptersVersion, len(rec.Commands))
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	sum, err := ttylog.Chapters(f, commands(rec))
	if err != nil {
		w.Header().Del("ETag")
		s.Logger.Error("unreadable recording", "sha256", rec.SHA256, "err", err)
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
