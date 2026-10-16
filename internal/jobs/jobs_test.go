package jobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// waitFor polls a job until its status leaves Running.
func waitFor(t *testing.T, m *Manager, id int64) Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := m.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status != Running {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %d still running", id)
	return Job{}
}

func TestOneAtATime(t *testing.T) {
	m := New(context.Background())
	release := make(chan struct{})
	first, err := m.Start("slow", nil, false, func(ctx context.Context, progress func(Progress)) (any, error) {
		progress(Progress{Done: 1, Total: 2})
		<-release
		return "result", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Running: another is refused, with the running one.
	if j, err := m.Start("other", nil, false, nil); !errors.Is(err, ErrBusy) || j.ID != first.ID {
		t.Fatalf("second start = %+v, %v", j, err)
	}
	close(release)
	j := waitFor(t, m, first.ID)
	if j.Status != Done || j.Result != "result" || j.Finished == nil || j.Progress.Done != 1 {
		t.Errorf("job = %+v", j)
	}
	// Done: the next one starts.
	if _, err := m.Start("next", nil, false, func(context.Context, func(Progress)) (any, error) { return nil, nil }); err != nil {
		t.Errorf("after the first: %v", err)
	}
}

func TestCancelAndFailure(t *testing.T) {
	m := New(context.Background())
	j, _ := m.Start("purge", nil, true, func(ctx context.Context, _ func(Progress)) (any, error) {
		<-ctx.Done()
		return "partial", ctx.Err()
	})
	if _, err := m.Cancel(j.ID); err != nil {
		t.Fatal(err)
	}
	if j = waitFor(t, m, j.ID); j.Status != Canceled || j.Result != "partial" {
		t.Errorf("canceled job = %+v", j)
	}
	j, _ = m.Start("broken", nil, false, func(context.Context, func(Progress)) (any, error) { panic("boom") })
	if j = waitFor(t, m, j.ID); j.Status != Failed || j.Error == "" {
		t.Errorf("panicking job = %+v", j)
	}
	if _, err := m.Get(999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown job: %v", err)
	}
}

// Only the last ones are kept, latest first.
func TestKept(t *testing.T) {
	m := New(context.Background())
	for range kept + 5 {
		j, err := m.Start("quick", nil, false, func(context.Context, func(Progress)) (any, error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, m, j.ID)
	}
	l := m.List()
	if len(l) != kept || l[0].ID != kept+5 {
		t.Errorf("kept %d jobs, first %d", len(l), l[0].ID)
	}
}
