// Package worker runs Quack's background work: the case action queue and
// the periodic loops (appeal notifications, the audit mirror, transcript
// cleanup).
//
// The queue is only a latency shortcut. Case executions and notifications
// are stored before they are submitted, and a poller finds anything due that
// the queue dropped or a restart lost, so the database stays the source of
// truth.
package worker

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// Queue defaults for non-positive sizes, and the poller's pace.
const (
	defaultQueueSize = 1000
	pollInterval     = time.Second
	pollBatch        = 100
)

// Handler processes one case's due actions.
type Handler func(ctx context.Context, caseID string) error

// DueSource lists cases with executions or notifications that are due.
type DueSource interface {
	ListExecutableCaseIDs(ctx context.Context, limit int) ([]string, error)
}

// Worker is a bounded case queue with a database poller, plus periodic
// loops. It implements quack.Scheduler. Build it with New, add loops with
// Every, then Start it once and Stop it once.
type Worker struct {
	jobs      chan job
	workers   int
	pollEvery time.Duration
	loops     []loop

	mu         sync.Mutex
	started    bool
	active     bool
	pending    map[string]struct{} // cases queued or running
	handler    Handler
	workCtx    context.Context
	cancelPoll context.CancelFunc
	cancelWork context.CancelFunc
	queueWG    sync.WaitGroup
	loopWG     sync.WaitGroup
	done       chan struct{}

	enqueued, dropped, processed, failed, panicked atomic.Uint64
	lastProcessed                                  atomic.Pointer[string]
}

// job is one queued case and the trace IDs of the request that queued it.
type job struct {
	caseID, requestID, correlationID string
}

// loop is a function run every interval until Stop.
type loop struct {
	name     string
	interval time.Duration
	fn       func(context.Context) error
}

// New returns a worker with a queue of size cases and workers goroutines.
// Non-positive values get defaults of 1000 and 1.
func New(size, workers int) *Worker {
	if size <= 0 {
		size = defaultQueueSize
	}
	return &Worker{
		jobs:      make(chan job, size),
		workers:   max(workers, 1),
		pollEvery: pollInterval,
		pending:   make(map[string]struct{}),
		done:      make(chan struct{}),
	}
}

// Every adds a loop that runs fn immediately on Start and then every
// interval. A failed run is logged and retried on the next tick. Call it
// before Start.
func (w *Worker) Every(name string, interval time.Duration, fn func(context.Context) error) {
	w.loops = append(w.loops, loop{name: name, interval: interval, fn: fn})
}

// Start launches the queue workers, the poller, and the loops. Queued case
// work keeps ctx's values but not its cancellation, so Stop can drain it.
// A worker starts at most once.
func (w *Worker) Start(ctx context.Context, handler Handler, source DueSource) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	w.started, w.active, w.handler = true, true, handler
	w.workCtx, w.cancelWork = context.WithCancel(context.WithoutCancel(ctx))
	pollCtx, cancelPoll := context.WithCancel(ctx)
	w.cancelPoll = cancelPoll
	for range w.workers {
		w.queueWG.Add(1)
		go w.work()
	}
	if source != nil {
		w.loopWG.Add(1)
		go func() {
			defer w.loopWG.Done()
			every(pollCtx, w.pollEvery, func(ctx context.Context) { w.enqueueDue(ctx, source) })
		}()
	}
	for _, l := range w.loops {
		w.loopWG.Add(1)
		go func() {
			defer w.loopWG.Done()
			every(pollCtx, l.interval, func(ctx context.Context) {
				if err := l.fn(ctx); err != nil && ctx.Err() == nil {
					slog.ErrorContext(ctx, "Background loop failed", "loop", l.name, "error", err)
				}
			})
		}()
	}
}

// Submit queues a case for immediate processing and reports whether it was
// accepted. A case already waiting counts as accepted. A full or stopped
// queue drops it, leaving it for the poller.
func (w *Worker) Submit(ctx context.Context, caseID string) bool {
	if caseID == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.active {
		w.dropped.Add(1)
		return false
	}
	if _, ok := w.pending[caseID]; ok {
		return true
	}
	requestID := quack.RequestIDFromContext(ctx)
	if requestID == "" {
		requestID = quack.NewTraceID()
	}
	correlationID := quack.CorrelationIDFromContext(ctx)
	if correlationID == "" {
		correlationID = requestID
	}
	select {
	case w.jobs <- job{caseID: caseID, requestID: requestID, correlationID: correlationID}:
		w.pending[caseID] = struct{}{}
		w.enqueued.Add(1)
		return true
	default:
		w.dropped.Add(1)
		return false
	}
}

// Stop stops the poller and loops, stops accepting cases, and waits for
// queued cases to finish. If ctx ends first it cancels the running handlers
// and returns ctx's error. Concurrent and repeated calls wait for the same
// drain.
func (w *Worker) Stop(ctx context.Context) error {
	w.mu.Lock()
	if !w.started {
		w.mu.Unlock()
		return nil
	}
	if w.active {
		w.active = false
		w.cancelPoll()
		close(w.jobs)
		go func() {
			w.loopWG.Wait()
			w.queueWG.Wait()
			w.cancelWork()
			close(w.done)
		}()
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		w.cancelWork()
		return fmt.Errorf("stop worker: %w", ctx.Err())
	}
}

// Stats returns a snapshot of the queue's counters.
func (w *Worker) Stats() quack.QueueStats {
	w.mu.Lock()
	active := w.active
	w.mu.Unlock()
	stats := quack.QueueStats{
		Active:         active,
		Workers:        w.workers,
		QueueSize:      len(w.jobs),
		BufferSize:     cap(w.jobs),
		EnqueuedTotal:  w.enqueued.Load(),
		DroppedTotal:   w.dropped.Load(),
		ProcessedTotal: w.processed.Load(),
		FailedTotal:    w.failed.Load(),
		PanickedTotal:  w.panicked.Load(),
	}
	if last := w.lastProcessed.Load(); last != nil {
		stats.LastProcessedID = *last
		stats.LastProcessedType = "case_action_execution"
	}
	return stats
}

// enqueueDue submits one batch of due cases from source.
func (w *Worker) enqueueDue(ctx context.Context, source DueSource) {
	caseIDs, err := source.ListExecutableCaseIDs(ctx, pollBatch)
	if err != nil {
		if ctx.Err() == nil {
			slog.ErrorContext(ctx, "Failed to discover executable case actions", "error", err)
		}
		return
	}
	for _, caseID := range caseIDs {
		w.Submit(ctx, caseID)
	}
}

// work runs queued cases until Stop closes the queue.
func (w *Worker) work() {
	defer w.queueWG.Done()
	for next := range w.jobs {
		w.process(next)
	}
}

// process runs the handler for one case under the trace IDs it was queued
// with. A panic is contained and counted as a failure.
func (w *Worker) process(next job) {
	defer func() {
		w.mu.Lock()
		delete(w.pending, next.caseID)
		w.mu.Unlock()
		if recovered := recover(); recovered != nil {
			w.panicked.Add(1)
			w.failed.Add(1)
			slog.Error("Case action job panicked", "panic", recovered, "case_id", next.caseID,
				"request_id", next.requestID, "correlation_id", next.correlationID)
		}
	}()
	ctx := quack.ContextWithTrace(w.workCtx, next.requestID, next.correlationID)
	if err := w.handler(ctx, next.caseID); err != nil {
		w.failed.Add(1)
		slog.Error("Case action job failed", "error", err, "case_id", next.caseID,
			"request_id", next.requestID, "correlation_id", next.correlationID)
		return
	}
	w.processed.Add(1)
	w.lastProcessed.Store(&next.caseID)
}

// every runs fn now and then on each tick until ctx is done.
func every(ctx context.Context, interval time.Duration, fn func(context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		fn(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
