package api

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// statsCache keeps the answers of the statistics endpoints, by path and
// query, so that a page is slow only the first time it is asked for:
//
//   - missing, an answer is computed and kept; requests asking for it
//     meanwhile wait for that one computation rather than start their own;
//   - fresh (younger than ttl), it is served as it is;
//   - stale, it is still served at once, and computed again in the
//     background, once however many ask: the next request gets the new one.
//
// Answers unused for idle are dropped, then the least recently used beyond
// maxBytes. Only successful answers are kept. The statistics are thus
// late by a few minutes at most, which X-Cauri-Cache and Age tell.
type statsCache struct {
	ttl, idle time.Duration
	maxBytes  int
	root      http.Handler // the whole API, which background computations go through
	now       func() time.Time

	mu      sync.Mutex
	entries map[string]*cacheEntry
	size    int    // bytes of the bodies kept
	gen     uint64 // bumped by clear: computations started before are not kept
}

type cacheEntry struct {
	ready      chan struct{} // closed once first computed
	ok         bool          // computed, with a successful answer
	header     http.Header
	body       []byte
	at         time.Time // when computed
	used       time.Time // when last served
	refreshing bool
}

// Defaults of the cache: fresh for a minute, kept while used in the last
// half hour, 64 MiB of answers at most.
const (
	defaultStatsCacheIdle  = 30 * time.Minute
	defaultStatsCacheBytes = 64 << 20
	// refreshTimeout bounds a background computation.
	refreshTimeout = 5 * time.Minute
)

func newStatsCache(ttl time.Duration) *statsCache {
	return &statsCache{
		ttl: ttl, idle: defaultStatsCacheIdle, maxBytes: defaultStatsCacheBytes,
		now: time.Now, entries: map[string]*cacheEntry{},
	}
}

// cacheRefresh marks the requests of background computations.
type cacheRefresh struct{}

// middleware serves the requests it wraps from the cache.
func (c *statsCache) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			next.ServeHTTP(w, r)
			return
		}
		key := r.URL.Path + "?" + r.URL.Query().Encode()
		if r.Context().Value(cacheRefresh{}) != nil {
			c.compute(key, next, r)
			return
		}

		c.mu.Lock()
		e := c.entries[key]
		if e == nil {
			e = &cacheEntry{ready: make(chan struct{})}
			c.entries[key] = e
			gen := c.gen
			c.mu.Unlock()
			// Computed apart from this request: another one may be waiting
			// for it, and it is worth keeping even if this client leaves.
			rec := record(next, r.WithContext(context.WithoutCancel(r.Context())))
			c.store(key, e, rec, gen)
			serve(w, rec.header, rec.status, rec.body.Bytes(), "miss", 0)
			return
		}
		c.mu.Unlock()

		select {
		case <-e.ready:
		case <-r.Context().Done():
			return
		}
		c.mu.Lock()
		if !e.ok {
			// The computation failed, and was not kept: try again.
			c.mu.Unlock()
			next.ServeHTTP(w, r)
			return
		}
		now := c.now()
		e.used = now
		age := now.Sub(e.at)
		state := "hit"
		if age >= c.ttl {
			state = "stale"
			if !e.refreshing {
				e.refreshing = true
				// Taken now: the request is done with once answered.
				u := *r.URL
				go c.refresh(key, &u, r.Header.Clone(), r.RemoteAddr) //nolint:gosec // G118: the refresh outlives the request, whose context chi's would reuse

			}
		}
		header, body := e.header, e.body
		c.mu.Unlock()
		serve(w, header, http.StatusOK, body, state, age)
	})
}

// compute answers a background computation's request and keeps the answer.
func (c *statsCache) compute(key string, next http.Handler, r *http.Request) {
	c.mu.Lock()
	gen := c.gen
	e := c.entries[key]
	c.mu.Unlock()
	rec := record(next, r)
	// Only an answer still kept is refreshed: one cleared meanwhile, maybe
	// being computed afresh, is not this computation's.
	if e != nil && e.ok {
		c.store(key, e, rec, gen)
	}
}

// refresh computes an answer again, through the whole API as a new request
// would: the one that found it stale is answered and gone.
// It is served in the process, never sent: built as the server builds a
// request it received.
func (c *statsCache) refresh(key string, u *url.URL, header http.Header, remoteAddr string) {
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), cacheRefresh{}, true), refreshTimeout)
	defer cancel()
	req := (&http.Request{
		Method: http.MethodGet, URL: u, RequestURI: u.RequestURI(), Host: u.Host,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: header, Body: http.NoBody, RemoteAddr: remoteAddr,
	}).WithContext(ctx)
	c.root.ServeHTTP(discard{}, req)
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.entries[key]; e != nil {
		e.refreshing = false
	}
}

// store keeps a computed answer, if successful and not cleared meanwhile.
func (c *statsCache) store(key string, e *cacheEntry, rec *recorder, gen uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	first := !e.ok
	defer func() {
		if first {
			close(e.ready)
		}
	}()
	if rec.status != http.StatusOK || gen != c.gen {
		if first && c.entries[key] == e {
			delete(c.entries, key)
		}
		return
	}
	now := c.now()
	if e.ok {
		c.size -= len(e.body)
	}
	e.ok, e.header, e.body, e.at = true, rec.header.Clone(), rec.body.Bytes(), now
	if first {
		e.used = now
	}
	c.size += len(e.body)
	c.evict(now)
}

// evict drops the answers unused for idle, then the least recently used
// while over maxBytes. Called with mu held.
func (c *statsCache) evict(now time.Time) {
	for key, e := range c.entries {
		if e.ok && now.Sub(e.used) > c.idle {
			c.drop(key, e)
		}
	}
	for c.size > c.maxBytes {
		var oldest string
		var at time.Time
		for key, e := range c.entries {
			if e.ok && (oldest == "" || e.used.Before(at)) {
				oldest, at = key, e.used
			}
		}
		if oldest == "" {
			return
		}
		c.drop(oldest, c.entries[oldest])
	}
}

func (c *statsCache) drop(key string, e *cacheEntry) {
	c.size -= len(e.body)
	delete(c.entries, key)
}

// clear forgets every answer, after a change that makes them wrong at once
// (sessions purged). Computations under way are not kept.
func (c *statsCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	for key, e := range c.entries {
		if e.ok {
			c.drop(key, e)
		}
	}
}

// recorder keeps an answer to serve it later.
type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func record(h http.Handler, r *http.Request) *recorder {
	rec := &recorder{header: http.Header{}, status: http.StatusOK}
	h.ServeHTTP(rec, r)
	return rec
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) WriteHeader(status int)      { r.status = status }
func (r *recorder) Write(p []byte) (int, error) { return r.body.Write(p) }

// serve writes an answer, saying whether it came from the cache and how old
// it is.
func serve(w http.ResponseWriter, header http.Header, status int, body []byte, state string, age time.Duration) {
	for k, v := range header {
		w.Header()[k] = v
	}
	w.Header().Set("X-Cauri-Cache", state)
	w.Header().Set("Age", strconv.Itoa(int(age.Seconds())))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// discard is the client of background computations.
type discard struct{}

func (discard) Header() http.Header         { return http.Header{} }
func (discard) WriteHeader(int)             {}
func (discard) Write(p []byte) (int, error) { return len(p), nil }
