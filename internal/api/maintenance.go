package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/render"

	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/jobs"
	"github.com/devleesch001/cauri/internal/purge"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/ttystore"
)

// The administration of the database: what it holds, what a purge would
// delete (a GET, which changes nothing), the purges and the compaction
// themselves, run as background tasks (package jobs) that the UI follows,
// and the configuration of the API.

// Task kinds.
const (
	taskPurgeSessions = "purge-sessions"
	taskPurgeRaw      = "purge-raw-events"
	taskVacuum        = "vacuum"
)

type databaseAnswer struct {
	*store.DatabaseInfo
	Recordings *ttystore.Usage `json:"recordings"` // null when the volume cannot be read
}

// getDatabase describes the database and the recordings volume.
func (s *server) getDatabase(w http.ResponseWriter, r *http.Request) {
	d, err := store.GetDatabaseInfo(r.Context(), s.DB)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := databaseAnswer{DatabaseInfo: d}
	if s.TTY != nil {
		if u, err := s.TTY.Usage(); err == nil {
			out.Recordings = &u
		} else {
			s.Logger.Warn("recordings volume unreadable", "err", err)
		}
	}
	render.JSON(w, r, out)
}

type configAnswer struct {
	Options []envflag.Option `json:"options"`
	// UnknownEnv are CAURI_ variables no option reads: a misspelt name
	// is silently ignored otherwise. Names only.
	UnknownEnv []string `json:"unknown_env"`
}

// getConfig lists the options of this API, secrets masked by the caller.
func (s *server) getConfig(w http.ResponseWriter, r *http.Request) {
	out := configAnswer{Options: s.Options, UnknownEnv: s.UnknownEnv}
	if out.Options == nil {
		out.Options = []envflag.Option{}
	}
	if out.UnknownEnv == nil {
		out.UnknownEnv = []string{}
	}
	render.JSON(w, r, out)
}

// purgeFilter reads the sessions to purge: the filters of the session
// list, its page and sort ignored.
func purgeFilter(w http.ResponseWriter, r *http.Request) (store.SessionFilter, bool) {
	q := query{v: r.URL.Query()}
	f := sessionFilter(&q)
	if q.err != nil {
		render.Render(w, r, errorOf(http.StatusBadRequest, q.err.Error()))
		return f, false
	}
	return f, true
}

// countPurgeSessions tells what deleting the sessions would remove: the
// simulation of DELETE with the same query.
func (s *server) countPurgeSessions(w http.ResponseWriter, r *http.Request) {
	f, ok := purgeFilter(w, r)
	if !ok {
		return
	}
	c, err := purge.New(s.DB, s.TTY).Sessions(r.Context(), f, false)
	if errors.Is(err, purge.ErrNoCriteria) {
		render.Render(w, r, errorOf(http.StatusBadRequest, err.Error()))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	render.JSON(w, r, c)
}

// purgeSessions starts deleting the sessions, everything about them and
// what no other session uses.
func (s *server) purgeSessions(w http.ResponseWriter, r *http.Request) {
	f, ok := purgeFilter(w, r)
	if !ok {
		return
	}
	p := purge.New(s.DB, s.TTY)
	// Counted first, for the progress; also refuses a purge of everything.
	total, err := p.Sessions(r.Context(), f, false)
	if errors.Is(err, purge.ErrNoCriteria) {
		render.Render(w, r, errorOf(http.StatusBadRequest, err.Error()))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.startTask(w, r, taskPurgeSessions, r.URL.Query(), true, func(ctx context.Context, progress func(jobs.Progress)) (any, error) {
		return p.SessionsProgress(ctx, f, true, func(c store.PurgeCounts) {
			progress(jobs.Progress{Done: c.Sessions, Total: total.Sessions})
		})
	})
}

// rawBefore reads the age of the raw lines to purge, in whole days.
func rawBefore(w http.ResponseWriter, r *http.Request) (time.Time, int, bool) {
	days, err := strconv.Atoi(r.URL.Query().Get("older_than_days"))
	if err != nil || days < 0 {
		render.Render(w, r, errorOf(http.StatusBadRequest, "older_than_days: want a number of days, 0 or more"))
		return time.Time{}, 0, false
	}
	return time.Now().AddDate(0, 0, -days), days, true
}

// countPurgeRaw tells what purging the raw lines would remove.
func (s *server) countPurgeRaw(w http.ResponseWriter, r *http.Request) {
	before, _, ok := rawBefore(w, r)
	if !ok {
		return
	}
	c, err := store.CountRawEvents(r.Context(), s.DB, before)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	render.JSON(w, r, c)
}

// purgeRaw starts deleting the raw lines older than the days given; what
// the tables hold of them stays (see store.PurgeRawEvents).
func (s *server) purgeRaw(w http.ResponseWriter, r *http.Request) {
	before, days, ok := rawBefore(w, r)
	if !ok {
		return
	}
	c, err := store.CountRawEvents(r.Context(), s.DB, before)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	params := map[string]any{"older_than_days": days, "before": before.UTC()}
	s.startTask(w, r, taskPurgeRaw, params, true, func(ctx context.Context, progress func(jobs.Progress)) (any, error) {
		n, err := store.PurgeRawEventsProgress(ctx, s.DB, before, func(deleted int) {
			progress(jobs.Progress{Done: deleted, Total: int(c.Lines)})
		})
		return map[string]int{"lines": n}, err
	})
}

// vacuum starts compacting the database; it cannot be canceled.
func (s *server) vacuum(w http.ResponseWriter, r *http.Request) {
	s.startTask(w, r, taskVacuum, nil, false, func(ctx context.Context, _ func(jobs.Progress)) (any, error) {
		size := func() int64 {
			d, err := store.GetDatabaseInfo(ctx, s.DB)
			if err != nil {
				return 0
			}
			return d.Size
		}
		before := size()
		if err := store.Vacuum(ctx, s.DB); err != nil {
			return nil, err
		}
		return map[string]int64{"size_before": before, "size_after": size()}, nil
	})
}

// startTask runs a task in the background, answering 202 and where to
// follow it, or 409 if one runs already. Its start and end are logged,
// with who asked.
func (s *server) startTask(w http.ResponseWriter, r *http.Request, kind string, params any, cancelable bool, task jobs.Task) {
	client := middleware.GetClientIP(r.Context())
	logged := func(ctx context.Context, progress func(jobs.Progress)) (any, error) {
		start := time.Now()
		result, err := task(ctx, progress)
		s.Logger.Info("administration task ended", "kind", kind, "params", params, "client", client,
			"result", result, "err", err, "duration_ms", time.Since(start).Milliseconds())
		return result, err
	}
	j, err := s.Jobs.Start(kind, params, cancelable, logged)
	if errors.Is(err, jobs.ErrBusy) {
		render.Render(w, r, errorOf(http.StatusConflict, fmt.Sprintf("task %d (%s) is running: one at a time", j.ID, j.Kind)))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Logger.Info("administration task started", "kind", kind, "id", j.ID, "params", params, "client", client)
	w.Header().Set("Location", fmt.Sprintf("%s/jobs/%d", adminPath, j.ID))
	render.Status(r, http.StatusAccepted)
	render.JSON(w, r, j)
}

func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	render.JSON(w, r, s.Jobs.List())
}

// jobOf answers the job of the URL with get, or 404.
func (s *server) jobOf(w http.ResponseWriter, r *http.Request, get func(int64) (jobs.Job, error)) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		render.Render(w, r, errorOf(http.StatusNotFound, "no such task"))
		return
	}
	j, err := get(id)
	if errors.Is(err, jobs.ErrNotFound) {
		render.Render(w, r, errorOf(http.StatusNotFound, "no such task"))
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	render.JSON(w, r, j)
}

func (s *server) getJob(w http.ResponseWriter, r *http.Request) { s.jobOf(w, r, s.Jobs.Get) }

// cancelJob asks a running purge to stop between two batches.
func (s *server) cancelJob(w http.ResponseWriter, r *http.Request) {
	s.jobOf(w, r, s.Jobs.Cancel)
}
