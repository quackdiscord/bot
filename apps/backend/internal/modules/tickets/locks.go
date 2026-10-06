package tickets

import (
	"context"
	"sync"
)

// keyedLocks is a set of per-key mutexes whose wait honors a context. It
// serializes one ticket's close pipeline, or one thread's journal writes,
// within this process; different keys proceed independently.
type keyedLocks struct {
	mu      sync.Mutex
	entries map[string]*keyedLock
}

// keyedLock counts holders and waiters, so an entry is dropped only when
// nobody uses it and a cancelled waiter can never let a second holder in.
type keyedLock struct {
	token      chan struct{}
	references int
}

// acquire waits for key's lock and returns its release, or ctx's error if
// ctx ends first.
func (l *keyedLocks) acquire(ctx context.Context, key string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	if l.entries == nil {
		l.entries = make(map[string]*keyedLock)
	}
	lock := l.entries[key]
	if lock == nil {
		lock = &keyedLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		l.entries[key] = lock
	}
	lock.references++
	l.mu.Unlock()
	drop := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		lock.references--
		if lock.references == 0 {
			delete(l.entries, key)
		}
	}
	select {
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	case <-lock.token:
		if err := ctx.Err(); err != nil {
			lock.token <- struct{}{}
			drop()
			return nil, err
		}
		return func() {
			lock.token <- struct{}{}
			drop()
		}, nil
	}
}
