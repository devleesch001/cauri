package ingestor

import (
	"sync"
	"time"
)

// ttyJob is a TTY recording waiting to be uploaded.
type ttyJob struct {
	Session  string    `json:"session"`
	TTYLog   string    `json:"ttylog"` // path written by Cowrie; only its base name is used
	Attempts int       `json:"attempts"`
	NextTry  time.Time `json:"next_try"`
}

// ttyQueue is the persistent list of pending uploads, one per session.
type ttyQueue struct {
	path   string
	mu     sync.Mutex
	jobs   []ttyJob
	notify chan struct{}
}

func loadTTYQueue(path string) (*ttyQueue, error) {
	q := &ttyQueue{path: path, notify: make(chan struct{}, 1)}
	_, err := loadJSON(path, &q.jobs)
	return q, err
}

// add queues jobs not already queued and saves the queue.
func (q *ttyQueue) add(jobs ...ttyJob) error {
	if len(jobs) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, j := range jobs {
		if q.find(j.Session) < 0 {
			q.jobs = append(q.jobs, j)
		}
	}
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return saveJSON(q.path, q.jobs)
}

// next returns the job due first, and whether it is due at now.
func (q *ttyQueue) next(now time.Time) (ttyJob, bool, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		return ttyJob{}, false, false
	}
	best := 0
	for i, j := range q.jobs {
		if j.NextTry.Before(q.jobs[best].NextTry) {
			best = i
		}
	}
	j := q.jobs[best]
	return j, true, !j.NextTry.After(now)
}

// done removes the job of a session and saves the queue.
func (q *ttyQueue) done(session string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if i := q.find(session); i >= 0 {
		q.jobs = append(q.jobs[:i], q.jobs[i+1:]...)
	}
	return saveJSON(q.path, q.jobs)
}

// retry postpones the job of a session and saves the queue.
func (q *ttyQueue) retry(session string, at time.Time) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if i := q.find(session); i >= 0 {
		q.jobs[i].Attempts++
		q.jobs[i].NextTry = at
	}
	return saveJSON(q.path, q.jobs)
}

func (q *ttyQueue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.jobs)
}

func (q *ttyQueue) find(session string) int {
	for i, j := range q.jobs {
		if j.Session == session {
			return i
		}
	}
	return -1
}
