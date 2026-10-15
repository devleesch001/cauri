package ingestor

import (
	"slices"
	"sync"
	"time"
)

// sessionWait is how long a session's recordings wait for its
// cowrie.session.closed, after its last channel closed, before being sent
// anyway: Cowrie may stop before writing it.
const sessionWait = time.Hour

// maxChannels bounds the channels kept per session: only whether there is
// more than one matters, and the attacker chooses how many it opens.
const maxChannels = 2

// ttyChannel is the recording of one SSH channel of a session, as its
// cowrie.log.closed tells it.
type ttyChannel struct {
	TTYLog     string    `json:"ttylog"` // path written by Cowrie; only its base name is used
	ClosedAt   time.Time `json:"closed_at"`
	DurationMS int64     `json:"duration_ms"`
	// Duplicate: Cowrie kept the file of an earlier recording of the same
	// input, with that one's times.
	Duplicate bool `json:"duplicate"`
}

// ttyJob is the recording of a session waiting to be uploaded: its
// channels, of which one alone is sent.
type ttyJob struct {
	Session  string       `json:"session"`
	Channels []ttyChannel `json:"channels,omitempty"`
	// Closed: the session's cowrie.session.closed was delivered, no
	// channel will come.
	Closed bool `json:"closed,omitempty"`
	// TTYLog is the single recording of a job queued by an older version.
	TTYLog   string    `json:"ttylog,omitempty"`
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
	for i, j := range q.jobs {
		if len(j.Channels) == 0 && j.TTYLog != "" {
			q.jobs[i].Channels, q.jobs[i].Closed, q.jobs[i].TTYLog = []ttyChannel{{TTYLog: j.TTYLog}}, true, ""
		}
	}
	return q, err
}

// sessionEvent is what the queue needs of a delivered event: a channel's
// recording closed, or its session closed (channel nil).
type sessionEvent struct {
	session string
	channel *ttyChannel
}

// record adds delivered events to the queue, in order, and saves it. A
// session's job waits for its cowrie.session.closed, or sessionWait after
// its last channel. Events sent again add nothing.
func (q *ttyQueue) record(events []sessionEvent, now time.Time) error {
	if len(events) == 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, e := range events {
		i := q.find(e.session)
		if e.channel == nil {
			if i >= 0 && !q.jobs[i].Closed {
				q.jobs[i].Closed, q.jobs[i].NextTry = true, now
			}
			continue
		}
		if i < 0 {
			q.jobs = append(q.jobs, ttyJob{Session: e.session})
			i = len(q.jobs) - 1
		}
		j := &q.jobs[i]
		if !slices.ContainsFunc(j.Channels, func(c ttyChannel) bool {
			return c.TTYLog == e.channel.TTYLog && c.ClosedAt.Equal(e.channel.ClosedAt)
		}) && len(j.Channels) < maxChannels {
			j.Channels = append(j.Channels, *e.channel)
		}
		if !j.Closed {
			j.NextTry = now.Add(sessionWait)
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
