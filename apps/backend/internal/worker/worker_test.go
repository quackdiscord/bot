package worker

import (
	"context"
	"sync"
	"testing"
	"time"
)

type dueSource struct {
	mu      sync.Mutex
	caseIDs []string
}

func (s *dueSource) ListExecutableCaseIDs(context.Context, int) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]string(nil), s.caseIDs...)
	s.caseIDs = nil
	return out, nil
}

func TestQueueProcessesSubmittedAndDiscoveredWork(t *testing.T) {
	source := &dueSource{caseIDs: []string{"due-1"}}
	processed := make(chan string, 2)
	q := New(4, 1)
	q.pollEvery = 10 * time.Millisecond
	q.Start(context.Background(), func(_ context.Context, caseID string) error {
		processed <- caseID
		return nil
	}, source)
	if !q.Submit(context.Background(), "direct-1") {
		t.Fatal("expected direct job to be accepted")
	}
	defer q.Stop(context.Background())

	got := map[string]bool{}
	for len(got) < 2 {
		select {
		case caseID := <-processed:
			got[caseID] = true
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for jobs: %+v", got)
		}
	}
	if !got["direct-1"] || !got["due-1"] {
		t.Fatalf("unexpected jobs: %+v", got)
	}
}

func TestQueueStopDrainsWithoutDeadlock(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	q := New(1, 1)
	q.Start(context.Background(), func(context.Context, string) error {
		close(started)
		<-release
		return nil
	}, nil)
	q.Submit(context.Background(), "case-1")
	<-started

	stopped := make(chan struct{})
	go func() {
		q.Stop(context.Background())
		close(stopped)
	}()
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("queue shutdown deadlocked")
	}
	if q.Stats().Active {
		t.Fatal("queue remained active after stop")
	}
}

func TestQueueStopContextCancelsActiveDependencyWork(t *testing.T) {
	started := make(chan struct{})
	q := New(1, 1)
	q.Start(context.Background(), func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	q.Submit(context.Background(), "case-1")
	<-started
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := q.Stop(shutdownCtx); err == nil {
		t.Fatal("expected active handler to be bounded by shutdown deadline")
	}
}

func TestQueueSaturationLeavesWorkDiscoverable(t *testing.T) {
	q := New(1, 1)
	block := make(chan struct{})
	started := make(chan struct{})
	var startedOnce sync.Once
	q.Start(context.Background(), func(context.Context, string) error {
		startedOnce.Do(func() { close(started) })
		<-block
		return nil
	}, nil)
	if !q.Submit(context.Background(), "one") {
		t.Fatal("expected first immediate hint to be accepted")
	}
	<-started
	if !q.Submit(context.Background(), "two") {
		t.Fatal("expected second immediate hint to fill the queue")
	}
	if q.Submit(context.Background(), "three") {
		t.Fatal("expected saturated queue to reject immediate hint")
	}
	close(block)
	q.Stop(context.Background())
	if q.Stats().DroppedTotal == 0 {
		t.Fatal("expected dropped hint to be counted")
	}
}

func TestQueueRecoversHandlerPanic(t *testing.T) {
	q := New(1, 1)
	q.Start(context.Background(), func(context.Context, string) error {
		panic("boom")
	}, nil)
	q.Submit(context.Background(), "case-1")
	deadline := time.Now().Add(time.Second)
	for q.Stats().PanickedTotal == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	q.Stop(context.Background())
	stats := q.Stats()
	if stats.PanickedTotal != 1 || stats.FailedTotal != 1 {
		t.Fatalf("unexpected panic stats: %+v", stats)
	}
}

func TestQueueCoalescesPendingWork(t *testing.T) {
	q := New(1, 1)
	started, release := make(chan struct{}), make(chan struct{})
	q.Start(context.Background(), func(context.Context, string) error {
		close(started)
		<-release
		return nil
	}, nil)
	q.Submit(context.Background(), "case-1")
	<-started
	for range 100 {
		if !q.Submit(context.Background(), "case-1") {
			t.Fatal("pending work should be accepted without another slot")
		}
	}
	close(release)
	q.Stop(context.Background())
	if stats := q.Stats(); stats.EnqueuedTotal != 1 || stats.ProcessedTotal != 1 {
		t.Fatalf("duplicate work was queued: %+v", stats)
	}
}

func TestQueueCannotRestartAfterStop(t *testing.T) {
	q := New(1, 1)
	handler := func(context.Context, string) error { return nil }
	q.Start(context.Background(), handler, nil)
	q.Stop(context.Background())
	q.Start(context.Background(), handler, nil)
	if q.Stats().Active || q.Submit(context.Background(), "case-1") {
		t.Fatal("a stopped queue must remain closed")
	}
	q.Stop(context.Background())
}

func TestConcurrentStopWaitsForWorkers(t *testing.T) {
	q := New(1, 1)
	started := make(chan struct{})
	q.Start(context.Background(), func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	q.Submit(context.Background(), "case-1")
	<-started
	// The first caller starts a graceful drain. The second must wait for that
	// same drain, and its deadline must cancel the in-flight dependency.
	stopped := make(chan struct{})
	go func() { q.Stop(context.Background()); close(stopped) }()
	deadline, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := q.Stop(deadline); err == nil {
		t.Fatal("shutdown returned before the active handler completed")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("concurrent shutdown did not release the first caller")
	}
}
