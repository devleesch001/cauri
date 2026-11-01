package api

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// counter answers how many times it was called, after waiting for gate if
// set; fail makes it answer 500.
type counter struct {
	calls atomic.Int32
	gate  chan struct{}
	fail  atomic.Bool
}

func (h *counter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	n := h.calls.Add(1)
	if h.gate != nil {
		<-h.gate
	}
	if h.fail.Load() {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, "%d", n)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestCache(h http.Handler) (*statsCache, http.Handler, *clock) {
	clk := &clock{t: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)}
	c := newStatsCache(time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.now = clk.now
	handler := c.middleware(h)
	c.root = handler
	return c, handler, clk
}

func ask(t *testing.T, h http.Handler, path string) (body, state string) {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Body.String(), w.Header().Get("X-Cauri-Cache")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Slow only the first time: then fresh, then stale but served at once and
// computed again in the background, once.
func TestStatsCache(t *testing.T) {
	h := &counter{}
	_, cached, clk := newTestCache(h)
	if body, state := ask(t, cached, "/api/stats/summary?sensor=a"); body != "1" || state != "miss" {
		t.Fatalf("first = %s %s", body, state)
	}
	if body, state := ask(t, cached, "/api/stats/summary?sensor=a"); body != "1" || state != "hit" {
		t.Errorf("second = %s %s", body, state)
	}
	// Another query is another answer, whatever the order of its parameters.
	if body, _ := ask(t, cached, "/api/stats/summary?sensor=b"); body != "2" {
		t.Errorf("other query = %s", body)
	}
	if _, state := ask(t, cached, "/api/stats/summary?verdict=bot&sensor=b"); state != "miss" {
		t.Errorf("third query = %s", state)
	}
	if _, state := ask(t, cached, "/api/stats/summary?sensor=b&verdict=bot"); state != "hit" {
		t.Errorf("same query, parameters in another order = %s", state)
	}

	clk.add(2 * time.Minute)
	if body, state := ask(t, cached, "/api/stats/summary?sensor=a"); body != "1" || state != "stale" {
		t.Errorf("expired = %s %s, want the last answer at once", body, state)
	}
	waitFor(t, "the refresh", func() bool { body, _ := ask(t, cached, "/api/stats/summary?sensor=a"); return body == "4" })
	if body, state := ask(t, cached, "/api/stats/summary?sensor=a"); body != "4" || state != "hit" || h.calls.Load() != 4 {
		t.Errorf("after the refresh = %s %s, %d computations", body, state, h.calls.Load())
	}
}

// Requests for an answer being computed wait for that computation; a
// stale answer is computed again once however many ask.
func TestStatsCacheOnce(t *testing.T) {
	h := &counter{gate: make(chan struct{})}
	_, cached, clk := newTestCache(h)
	var wg sync.WaitGroup
	bodies := make([]string, 5)
	for i := range bodies {
		wg.Go(func() { bodies[i], _ = ask(t, cached, "/api/stats/credentials") })
	}
	waitFor(t, "the computation", func() bool { return h.calls.Load() == 1 })
	time.Sleep(20 * time.Millisecond)
	close(h.gate)
	wg.Wait()
	for _, b := range bodies {
		if b != "1" {
			t.Fatalf("bodies = %v, %d computations", bodies, h.calls.Load())
		}
	}

	h.gate = make(chan struct{})
	clk.add(2 * time.Minute)
	for range 5 {
		if body, state := ask(t, cached, "/api/stats/credentials"); body != "1" || state != "stale" {
			t.Fatalf("stale = %s %s", body, state)
		}
	}
	waitFor(t, "the refresh", func() bool { return h.calls.Load() == 2 })
	close(h.gate)
	waitFor(t, "the new answer", func() bool { body, _ := ask(t, cached, "/api/stats/credentials"); return body == "2" })
	if n := h.calls.Load(); n != 2 {
		t.Errorf("%d computations, want 2", n)
	}
}

// Failures are not kept, nor do they replace a kept answer.
func TestStatsCacheFailures(t *testing.T) {
	h := &counter{}
	h.fail.Store(true)
	_, cached, clk := newTestCache(h)
	ask(t, cached, "/api/stats/activity")
	if _, state := ask(t, cached, "/api/stats/activity"); state != "miss" {
		t.Errorf("after a failure = %s", state)
	}
	h.fail.Store(false)
	if body, _ := ask(t, cached, "/api/stats/activity"); body != "3" {
		t.Errorf("recovered = %s", body)
	}
	h.fail.Store(true)
	clk.add(2 * time.Minute)
	ask(t, cached, "/api/stats/activity")
	waitFor(t, "the failed refresh", func() bool { return h.calls.Load() == 4 })
	if body, _ := ask(t, cached, "/api/stats/activity"); body != "3" {
		t.Errorf("after a failed refresh = %s, want the last good answer", body)
	}
}

// A purge clears the cache; answers unused for long are dropped.
func TestStatsCacheClearAndIdle(t *testing.T) {
	h := &counter{}
	c, cached, clk := newTestCache(h)
	ask(t, cached, "/api/countries")
	c.clear()
	if _, state := ask(t, cached, "/api/countries"); state != "miss" {
		t.Errorf("after clear = %s", state)
	}
	clk.add(defaultStatsCacheIdle + time.Minute)
	ask(t, cached, "/api/cities") // keeping an answer drops the idle ones
	if _, state := ask(t, cached, "/api/countries"); state != "miss" {
		t.Errorf("after idle = %s", state)
	}
	// Other methods go through.
	w := httptest.NewRecorder()
	cached.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/countries", nil))
	if w.Header().Get("X-Cauri-Cache") != "" {
		t.Error("POST cached")
	}
}
