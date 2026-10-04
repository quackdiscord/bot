package modules_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
)

func TestPoolSubmitIsSafeDuringStop(t *testing.T) {
	var handled, accepted atomic.Int64
	pool := modules.NewPool("test", 128, 2, func(context.Context, string) { handled.Add(1) })
	pool.Start(context.Background())
	var submitters sync.WaitGroup
	for range 64 {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			if pool.Submit("job") {
				accepted.Add(1)
			}
		}()
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	submitters.Wait()
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if handled.Load() != accepted.Load() {
		t.Fatalf("handled %d of %d accepted jobs", handled.Load(), accepted.Load())
	}
}

func TestPoolStopIsBoundedByContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	pool := modules.NewPool("test", 1, 1, func(ctx context.Context, _ int) {
		close(started)
		select {
		case <-ctx.Done():
		case <-release:
		}
	})
	pool.Start(context.Background())
	pool.Submit(1)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pool.Stop(ctx); err == nil {
		t.Fatal("Stop waited past its deadline")
	}
}
