package services

import (
	"sync"

	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

type EventQueue struct {
	Queue   chan structs.QueueEvent
	workers int
	wg      sync.WaitGroup
	active  bool
	mu      sync.RWMutex
}

var EQ *EventQueue

var s *storage.Store

// Initializes the event queue
func (q *EventQueue) Init(st *storage.Store) {
	s = st
	EQ = &EventQueue{
		Queue:   make(chan structs.QueueEvent, lib.Config.EventQueue.Size),
		workers: lib.Config.EventQueue.Workers,
		active:  false,
	}
	log.Info().
		Int("buffer_size", lib.Config.EventQueue.Size).
		Int("workers", lib.Config.EventQueue.Workers).
		Msg("Event queue initialized")
}

// Start begins processing events from the queue with multiple workers
func (q *EventQueue) Start() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.active {
		log.Warn().Msg("Event queue already running")
		return
	}

	q.active = true
	log.Info().Int("workers", q.workers).Msg("Starting event queue workers")

	for i := 0; i < q.workers; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

// worker processes events from the queue
func (q *EventQueue) worker(id int) {
	defer q.wg.Done()

	log.Debug().Int("worker_id", id).Msg("Event queue worker started")

	for event := range q.Queue {
		q.Process(event)
	}

	log.Debug().Int("worker_id", id).Msg("Event queue worker stopped")
}

// Enqueue adds an event to the queue
func (q *EventQueue) Enqueue(event structs.QueueEvent) {
	q.mu.RLock()
	defer q.mu.RUnlock()

	if !q.active {
		log.Warn().
			Str("event_type", event.Type).
			Msg("Attempted to enqueue event but queue is not active")
		return
	}

	select {
	case q.Queue <- event:
	default:
		log.Warn().
			Str("event_type", event.Type).
			Msg("Event queue full, dropping event")
	}
}

// Process executes the event handler
func (q *EventQueue) Process(event structs.QueueEvent) {
	defer func() {
		if r := recover(); r != nil {
			log.Error().
				Str("event_type", event.Type).
				Interface("panic", r).
				Msg("Panic while processing event")
		}
	}()

	event.Handler(s, event.Data)
}

// Stop gracefully shuts down the event queue
func (q *EventQueue) Stop() {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.active {
		log.Warn().Msg("Event queue already stopped")
		return
	}

	log.Info().Msg("Stopping event queue")
	q.active = false
	close(q.Queue)
	q.wg.Wait()
	log.Info().Msg("Event queue stopped")
}

// IsActive returns whether the queue is currently active
func (q *EventQueue) IsActive() bool {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.active
}

// QueueSize returns the current number of events in the queue
func (q *EventQueue) QueueSize() int {
	return len(q.Queue)
}
