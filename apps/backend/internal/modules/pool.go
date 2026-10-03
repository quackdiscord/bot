package modules

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// Pool runs a module's gateway work on a few goroutines behind a bounded
// backlog. Gateway handlers must return quickly and module traffic must
// never delay moderation, so when the backlog is full Submit drops the job.
type Pool[T any] struct {
	name    string
	workers int
	handle  func(context.Context, T)
	jobs    chan T

	mu      sync.RWMutex
	closed  bool
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	stopped chan struct{}
	once    sync.Once
}

// NewPool returns a pool that runs handle on workers goroutines once
// started, holding at most capacity waiting jobs. name labels its logs.
func NewPool[T any](name string, capacity, workers int, handle func(context.Context, T)) *Pool[T] {
	return &Pool[T]{
		name:    name,
		workers: max(workers, 1),
		handle:  handle,
		jobs:    make(chan T, max(capacity, 1)),
		cancel:  func() {},
		stopped: make(chan struct{}),
	}
}

// Start launches the workers. Jobs run under ctx without its cancellation,
// so Stop can drain them; Stop cancels them only when its deadline passes.
func (p *Pool[T]) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	p.cancel = cancel
	for range p.workers {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				p.run(ctx, job)
			}
		}()
	}
}

// Submit queues job without blocking. It reports false when the backlog is
// full or the pool has stopped.
func (p *Pool[T]) Submit(job T) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.closed {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

// Stop stops accepting jobs and waits for queued ones to finish, or until
// ctx is done, when it cancels the jobs still running. It is safe to call
// more than once.
func (p *Pool[T]) Stop(ctx context.Context) error {
	p.once.Do(func() {
		p.mu.Lock()
		p.closed = true
		close(p.jobs)
		p.mu.Unlock()
		go func() {
			p.wg.Wait()
			close(p.stopped)
		}()
	})
	select {
	case <-p.stopped:
		p.cancel()
		return nil
	case <-ctx.Done():
		p.cancel()
		return ctx.Err()
	}
}

// run handles one job, containing a panic so one bad event cannot take down
// the pool. Message content is never logged.
func (p *Pool[T]) run(ctx context.Context, job T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.ErrorContext(ctx, "Module worker panicked", "module", p.name,
				"panic_type", fmt.Sprintf("%T", recovered), "stack", string(debug.Stack()))
		}
	}()
	p.handle(ctx, job)
}
