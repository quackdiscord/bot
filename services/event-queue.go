package services

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"
)

// Event represents a Discord event that needs to be processed
type Event struct {
	Type    string
	Data    interface{}
	GuildID string
}

// EventQueue manages the queueing and processing of Discord events
type EventQueue struct {
	queue     chan Event
	handlers  map[string]EventHandler
	mu        sync.RWMutex
	ctx       context.Context
	cancel    context.CancelFunc
	waitGroup sync.WaitGroup
}

// EventHandler is a function that processes an event
type EventHandler func(Event) error

var EQ *EventQueue

func ReadyEventQueue(size int) {
	EQ = NewEventQueue(size)
	log.Info().Msgf("Event queue ready with size %d", size)
}

// NewEventQueue creates a new event queue with the specified buffer size
func NewEventQueue(bufferSize int) *EventQueue {
	ctx, cancel := context.WithCancel(context.Background())
	return &EventQueue{
		queue:    make(chan Event, bufferSize),
		handlers: make(map[string]EventHandler),
		ctx:      ctx,
		cancel:   cancel,
	}
}

// RegisterHandler registers a handler function for a specific event type
func (eq *EventQueue) RegisterHandler(eventType string, handler EventHandler) {
	eq.mu.Lock()
	defer eq.mu.Unlock()
	eq.handlers[eventType] = handler
}

// Enqueue adds a new event to the queue
func (eq *EventQueue) Enqueue(event Event) {
	select {
	case eq.queue <- event:
		// event queued
	default:
		log.Error().Msg("Event queue is full")
	}
}

// Start begins processing events from the queue
func (eq *EventQueue) Start(numWorkers int) {
	for range numWorkers {
		eq.waitGroup.Add(1)
		go eq.worker()
	}
}

// Stop gracefully shuts down the event queue
func (eq *EventQueue) Stop() {
	eq.cancel()
	close(eq.queue)
	eq.waitGroup.Wait()
	log.Info().Msg("Event queue stopped")
}

// worker processes events from the queue
func (eq *EventQueue) worker() {
	defer eq.waitGroup.Done()

	for {
		select {
		case event, ok := <-eq.queue:
			if !ok {
				return
			}
			eq.processEvent(event)
		case <-eq.ctx.Done():
			return
		}
	}
}

// processEvent processes an event from the queue
func (eq *EventQueue) processEvent(event Event) {
	eq.mu.RLock()
	handler, exists := eq.handlers[event.Type]
	eq.mu.RUnlock()

	if !exists {
		log.Error().Msgf("No handler found for event type %s", event.Type)
		return
	}

	if err := handler(event); err != nil {
		log.Error().Err(err).Msgf("Error processing event type %s", event.Type)
	}
}

func (eq *EventQueue) GetQueueSize() int {
	eq.mu.RLock()
	defer eq.mu.RUnlock()

	return len(eq.queue)
}
