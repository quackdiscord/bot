package structs

import (
	"context"
	"sync"
)

type Event struct {
	Type    string
	Data    interface{}
	GuildID string
}

type EventQueue struct {
	Queue     chan Event
	Handlers  map[string]EventHandler
	Mu        sync.RWMutex
	Ctx       context.Context
	Cancel    context.CancelFunc
	WaitGroup sync.WaitGroup
}

type EventHandler func(Event) error
