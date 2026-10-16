// Package jobs runs the administration's long tasks (purges, compacting
// the database) in the background, one at a time: the request that starts
// one returns at once, and the task is followed by its id while it runs.
// Tasks live in memory: a restart forgets them and stops the one running,
// which every task must survive (purges delete by whole batches).
package jobs

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrBusy means a task is running already: tasks would hinder each other.
var ErrBusy = errors.New("a task is running")

// ErrNotFound means no task has this id, or it was forgotten.
var ErrNotFound = errors.New("no such task")

// Status of a task.
const (
	Running  = "running"
	Done     = "done"
	Failed   = "failed"
	Canceled = "canceled"
)

// kept bounds the finished tasks remembered.
const kept = 20

// Progress is how far a task got: done of total units (sessions, raw
// lines), total 0 when unknown.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// Job is a task, as it was when read.
type Job struct {
	ID         int64      `json:"id"`
	Kind       string     `json:"kind"`   // such as "purge-sessions"
	Params     any        `json:"params"` // what it was asked to do
	Status     string     `json:"status"`
	Cancelable bool       `json:"cancelable"`
	Started    time.Time  `json:"started"`
	Finished   *time.Time `json:"finished"`
	Progress   Progress   `json:"progress"`
	Result     any        `json:"result"` // set when done (or canceled, with what was done)
	Error      string     `json:"error,omitempty"`
}

// Task does the work of a job, reporting its progress, until done or ctx
// is canceled. It returns its result, even when it stops on an error.
type Task func(ctx context.Context, progress func(Progress)) (any, error)

// Manager runs one task at a time and remembers the last ones.
type Manager struct {
	ctx    context.Context // ends the running task when the process stops
	mu     sync.Mutex
	next   int64
	jobs   []*Job // oldest first
	cancel map[int64]context.CancelFunc
	wg     sync.WaitGroup
	now    func() time.Time
}

// New returns a manager whose tasks stop when ctx ends.
func New(ctx context.Context) *Manager {
	return &Manager{ctx: ctx, cancel: map[int64]context.CancelFunc{}, now: time.Now}
}

// Start runs task in the background as a job of kind, unless one runs
// already (ErrBusy, with that job). A cancelable task stops on Cancel.
func (m *Manager) Start(kind string, params any, cancelable bool, task Task) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.Status == Running {
			return *j, ErrBusy
		}
	}
	m.next++
	j := &Job{ID: m.next, Kind: kind, Params: params, Status: Running, Cancelable: cancelable, Started: m.now().UTC()}
	m.jobs = append(m.jobs, j)
	if len(m.jobs) > kept {
		m.jobs = m.jobs[len(m.jobs)-kept:]
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel[j.ID] = cancel
	m.wg.Add(1)
	go m.run(ctx, j, task)
	return *j, nil
}

func (m *Manager) run(ctx context.Context, j *Job, task Task) {
	defer m.wg.Done()
	var result any
	var err error
	func() {
		// A task that panics fails; the process goes on.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("task panicked: %v", r)
			}
		}()
		result, err = task(ctx, func(p Progress) {
			m.mu.Lock()
			j.Progress = p
			m.mu.Unlock()
		})
	}()
	m.mu.Lock()
	defer m.mu.Unlock()
	finished := m.now().UTC()
	j.Finished, j.Result = &finished, result
	switch {
	case err == nil:
		j.Status = Done
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		j.Status = Canceled
	default:
		j.Status, j.Error = Failed, err.Error()
	}
	m.cancel[j.ID]()
	delete(m.cancel, j.ID)
}

// Get returns the job of an id.
func (m *Manager) Get(id int64) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			return *j, nil
		}
	}
	return Job{}, ErrNotFound
}

// List returns the jobs remembered, latest first.
func (m *Manager) List() []Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for i := len(m.jobs) - 1; i >= 0; i-- {
		out = append(out, *m.jobs[i])
	}
	return out
}

// Cancel asks a running cancelable job to stop; it ends as Canceled once
// its task returns. A finished or uncancelable job is left as it is.
func (m *Manager) Cancel(id int64) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID != id {
			continue
		}
		if cancel, ok := m.cancel[id]; ok && j.Cancelable {
			cancel()
		}
		return *j, nil
	}
	return Job{}, ErrNotFound
}

// Wait waits for the running job to end, such as when the process stops.
func (m *Manager) Wait() { m.wg.Wait() }
