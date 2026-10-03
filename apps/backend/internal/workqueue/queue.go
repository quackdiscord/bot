package workqueue

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/quackdiscord/bot/internal/quack"
)

// Handler handles one unit of work through the package's transport-neutral callback contract.
type Handler func(context.Context, string) error

// DueSource discovers persisted actions whose initial attempt or retry is due.
type DueSource interface {
	ListExecutableCaseIDs(context.Context, int) ([]string, error)
}

// Queue combines a bounded latency queue with database polling, leaving persisted action rows as durable truth.
type Queue struct {
	jobs       chan job
	workers    int
	pollEvery  time.Duration
	batchSize  int
	mu         sync.RWMutex
	wg         sync.WaitGroup
	active     bool
	started    bool
	pending    map[string]struct{}
	done       chan struct{}
	handler    Handler
	source     DueSource
	cancelPoll context.CancelFunc
	cancelWork context.CancelFunc
	workerCtx  context.Context
	stats      quack.QueueStats
}

// job carries the case and trace identifiers needed by an action worker.
type job struct {
	caseID        string
	requestID     string
	correlationID string
}

// New creates a bounded queue with defensive defaults. The handler and durable source are supplied at Start so construction has no background side effects.
func New(size, workers int) *Queue {
	if size <= 0 {
		size = 1000
	}
	if workers <= 0 {
		workers = 1
	}
	return &Queue{
		jobs:      make(chan job, size),
		workers:   workers,
		pollEvery: time.Second,
		batchSize: 100,
		pending:   make(map[string]struct{}),
		done:      make(chan struct{}),
	}
}

// Start begins workers and polling once. A stopped queue cannot be restarted.
// Workers retain context values but drain independently of parent cancellation;
// the owner must call StopContext to bound shutdown.
func (q *Queue) Start(ctx context.Context, handler Handler, source DueSource) {
	q.mu.Lock()
	if q.started {
		q.mu.Unlock()
		return
	}
	q.active = true
	q.started = true
	q.handler = handler
	q.source = source
	workerCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	pollCtx, cancelPoll := context.WithCancel(ctx)
	q.cancelPoll = cancelPoll
	q.cancelWork = cancelWork
	q.workerCtx = workerCtx
	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker()
	}
	q.wg.Add(1)
	go q.poll(pollCtx)
	q.mu.Unlock()
}

// Submit offers immediate work to the queue as a latency optimization; persisted rows remain the durable source of truth.
func (q *Queue) Submit(ctx context.Context, caseID string) bool {
	if q == nil || caseID == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.active {
		atomic.AddUint64(&q.stats.DroppedTotal, 1)
		return false
	}
	if _, exists := q.pending[caseID]; exists {
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
	case q.jobs <- job{caseID: caseID, requestID: requestID, correlationID: correlationID}:
		q.pending[caseID] = struct{}{}
		atomic.AddUint64(&q.stats.EnqueuedTotal, 1)
		return true
	default:
		atomic.AddUint64(&q.stats.DroppedTotal, 1)
		return false
	}
}

// poll discovers persisted due work so actions recover after saturation or process restart.
func (q *Queue) poll(ctx context.Context) {
	defer q.wg.Done()
	q.enqueueDue(ctx)
	ticker := time.NewTicker(q.pollEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			q.enqueueDue(ctx)
		}
	}
}

// enqueueDue discovers persisted due work so actions recover after saturation or process restart.
func (q *Queue) enqueueDue(ctx context.Context) {
	q.mu.RLock()
	source := q.source
	batchSize := q.batchSize
	q.mu.RUnlock()
	if source == nil {
		return
	}
	caseIDs, err := source.ListExecutableCaseIDs(ctx, batchSize)
	if err != nil {
		slog.Error("Failed to discover executable case actions", "error", err)
		return
	}
	for _, caseID := range caseIDs {
		q.Submit(ctx, caseID)
	}
}

// worker consumes queued case work until graceful shutdown closes the work channel.
func (q *Queue) worker() {
	defer q.wg.Done()
	for next := range q.jobs {
		q.process(next)
	}
}

// process restores trace context, invokes the configured case handler, and updates queue metrics while containing handler panics inside the worker.
func (q *Queue) process(next job) {
	defer func() {
		q.mu.Lock()
		delete(q.pending, next.caseID)
		q.mu.Unlock()
		if recovered := recover(); recovered != nil {
			atomic.AddUint64(&q.stats.PanickedTotal, 1)
			atomic.AddUint64(&q.stats.FailedTotal, 1)
			slog.Error("Case action job panicked", "panic", recovered, "case_id", next.caseID, "request_id", next.requestID, "correlation_id", next.correlationID)
		}
	}()
	q.mu.RLock()
	handler := q.handler
	q.mu.RUnlock()
	if handler == nil {
		atomic.AddUint64(&q.stats.FailedTotal, 1)
		return
	}
	q.mu.RLock()
	workerCtx := q.workerCtx
	q.mu.RUnlock()
	if workerCtx == nil {
		workerCtx = context.Background()
	}
	ctx := quack.ContextWithTrace(workerCtx, next.requestID, next.correlationID)
	if err := handler(ctx, next.caseID); err != nil {
		atomic.AddUint64(&q.stats.FailedTotal, 1)
		slog.Error("Case action job failed", "error", err, "case_id", next.caseID, "request_id", next.requestID, "correlation_id", next.correlationID)
		return
	}
	atomic.AddUint64(&q.stats.ProcessedTotal, 1)
	q.mu.Lock()
	q.stats.LastProcessedID = next.caseID
	q.stats.LastProcessedType = "case_action_execution"
	q.mu.Unlock()
}

// Stop stops accepting queue work, halts polling, and drains workers before returning.
func (q *Queue) Stop() {
	_ = q.StopContext(context.Background())
}

// StopContext stops accepting work and waits only until the caller's shutdown
// deadline. Canceling the worker context interrupts dependency calls that honor
// Quack's context contract.
func (q *Queue) StopContext(ctx context.Context) error {
	if q == nil {
		return nil
	}
	q.mu.Lock()
	if !q.started {
		q.mu.Unlock()
		return nil
	}
	cancelWork := q.cancelWork
	if q.active {
		q.active = false
		q.cancelPoll()
		close(q.jobs)
		go func() {
			q.wg.Wait()
			cancelWork()
			close(q.done)
		}()
	}
	q.mu.Unlock()
	select {
	case <-q.done:
		return nil
	case <-ctx.Done():
		cancelWork()
		return ctx.Err()
	}
}

// IsActive reports whether the queue currently accepts immediate submissions.
func (q *Queue) IsActive() bool {
	if q == nil {
		return false
	}
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.active
}

// Stats returns a race-safe snapshot of queue counters and the most recently processed job.
func (q *Queue) Stats() quack.QueueStats {
	if q == nil {
		return quack.QueueStats{}
	}
	q.mu.RLock()
	stats := quack.QueueStats{
		Active:            q.active,
		Workers:           q.workers,
		QueueSize:         len(q.jobs),
		BufferSize:        cap(q.jobs),
		LastProcessedID:   q.stats.LastProcessedID,
		LastProcessedType: q.stats.LastProcessedType,
	}
	q.mu.RUnlock()
	stats.EnqueuedTotal = atomic.LoadUint64(&q.stats.EnqueuedTotal)
	stats.DroppedTotal = atomic.LoadUint64(&q.stats.DroppedTotal)
	stats.ProcessedTotal = atomic.LoadUint64(&q.stats.ProcessedTotal)
	stats.FailedTotal = atomic.LoadUint64(&q.stats.FailedTotal)
	stats.PanickedTotal = atomic.LoadUint64(&q.stats.PanickedTotal)
	return stats
}
