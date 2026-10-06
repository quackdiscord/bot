package honeypot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// cleanupApplier is a case applier that also deletes bait messages.
type cleanupApplier struct {
	*applierFake
	mu       sync.Mutex
	attempts map[string]int
	fail     bool
	// block, when set, makes deletes wait for their context to end.
	block bool
	// entered and release, when set, pause case creation to expose burst
	// races.
	entered chan struct{}
	release chan struct{}
}

func (a *cleanupApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	if a.entered != nil {
		close(a.entered)
		select {
		case <-a.release:
		case <-ctx.Done():
			return honeypot.ApplyResult{}, ctx.Err()
		}
	}
	return a.applierFake.ApplyHoneypotCase(ctx, request)
}

func (a *cleanupApplier) DeleteHoneypotMessage(ctx context.Context, _, messageID string) error {
	a.mu.Lock()
	a.attempts[messageID]++
	fail, block := a.fail, a.block
	a.mu.Unlock()
	if block {
		<-ctx.Done()
		return ctx.Err()
	}
	if fail {
		return errors.New("temporary Discord delete failure")
	}
	return nil
}

func (a *cleanupApplier) deleted() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.attempts)
}

// cleanupFixture is an enabled honeypot whose applier deletes messages.
func cleanupFixture(t *testing.T) (*fixture, *cleanupApplier) {
	t.Helper()
	f := setup(t)
	a := &cleanupApplier{applierFake: f.applier, attempts: map[string]int{}}
	f.service = honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	enable(t, f, "guild-a")
	return f, a
}

// Every message in a burst is deleted once, under one case, and replays
// after the burst window neither punish nor delete again.
func TestBurstCleanupRetainsOneCaseAndEveryMessage(t *testing.T) {
	f, a := cleanupFixture(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			_, err := f.service.HandleMessage(ctx, message(fmt.Sprintf("burst-%d", i)))
			if err != nil && !errors.Is(err, honeypot.ErrDuplicate) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := f.service.ProcessCleanups(ctx, 25); err != nil {
		t.Fatal(err)
	}
	if a.count() != 1 || a.deleted() != 20 {
		t.Fatalf("cases=%d deleted=%d", a.count(), a.deleted())
	}
	if err := f.db.Model(&honeypot.Trigger{}).Where("guild_id = ?", "guild-a").
		Update("created_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		if _, err := f.service.HandleMessage(ctx, message(fmt.Sprintf("burst-%d", i))); !errors.Is(err, honeypot.ErrDuplicate) {
			t.Fatalf("replay: %v", err)
		}
	}
	if a.count() != 1 {
		t.Fatal("a replay repeated the punishment")
	}
	if err := f.service.ProcessCleanups(ctx, 25); err != nil {
		t.Fatal(err)
	}
	for id, count := range a.attempts {
		if count != 1 {
			t.Fatalf("message %s deleted %d times", id, count)
		}
	}
}

// Bait stays until its incident has a saved case, and stays for good if the
// case fails, so the evidence is never lost.
func TestCleanupWaitsForSavedIncident(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint("failed=", failed), func(t *testing.T) {
			f, a := cleanupFixture(t)
			a.entered = make(chan struct{})
			a.release = make(chan struct{})
			if failed {
				a.applierFake.err = errors.New("case failed")
			}
			done := make(chan error, 1)
			go func() { _, err := f.service.HandleMessage(context.Background(), message("first")); done <- err }()
			<-a.entered
			if _, err := f.service.HandleMessage(context.Background(), message("second")); !errors.Is(err, honeypot.ErrDuplicate) {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			if a.deleted() != 0 {
				t.Fatal("deleted before the case was saved")
			}
			close(a.release)
			if err := <-done; (err != nil) != failed {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
				t.Fatal(err)
			}
			if want := map[bool]int{false: 2, true: 0}[failed]; a.deleted() != want {
				t.Fatalf("deleted %d messages, want %d", a.deleted(), want)
			}
		})
	}
}

// A failed delete stays due and is retried by a fresh service after a
// restart, without opening another case.
func TestCleanupRecoversAfterFailureAndRestart(t *testing.T) {
	f, a := cleanupFixture(t)
	if _, err := f.service.HandleMessage(context.Background(), message("retry")); err != nil {
		t.Fatal(err)
	}
	a.fail = true
	if err := f.service.ProcessCleanups(context.Background(), 1); err == nil {
		t.Fatal("delete failure hidden")
	}
	var record honeypot.MessageCleanup
	if err := f.db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.CompletedAt != nil || record.AttemptCount != 1 {
		t.Fatal("retry receipt lost", record)
	}
	if err := f.service.ProcessCleanups(context.Background(), 1); err != nil || a.attempts["retry"] != 1 {
		t.Fatalf("retried during backoff: attempts=%d err=%v", a.attempts["retry"], err)
	}
	if err := f.db.Model(&record).Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	a.fail = false
	restarted := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	if err := restarted.ProcessCleanups(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&record).Error; err != nil {
		t.Fatal(err)
	}
	if record.CompletedAt == nil || record.AttemptCount != 2 || a.count() != 1 {
		t.Fatal("restart repeated or lost work", record, a.count())
	}
}

// Staff, Quack, and webhook posts are never queued for deletion.
func TestCleanupNeverSchedulesExemptMessages(t *testing.T) {
	f, a := cleanupFixture(t)
	for i, exempt := range []func(*honeypot.Message){
		func(m *honeypot.Message) { m.AuthorCanModerate = true },
		func(m *honeypot.Message) { m.IsBot, m.AuthorCanModerate = true, true },
		func(m *honeypot.Message) { m.IsWebhook = true },
		func(m *honeypot.Message) { m.IsQuack = true },
	} {
		event := message(fmt.Sprintf("exempt-%d", i))
		exempt(&event)
		if _, err := f.service.HandleMessage(context.Background(), event); !errors.Is(err, honeypot.ErrExempt) {
			t.Fatal(err)
		}
	}
	if err := f.service.ProcessCleanups(context.Background(), 25); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&honeypot.MessageCleanup{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || a.deleted() != 0 || a.count() != 0 {
		t.Fatal("an exempt message was queued", count, a.attempts)
	}
}

// A delete cut off by shutdown keeps its receipt, due again once its lease
// expires.
func TestInterruptedCleanupStaysRecoverable(t *testing.T) {
	f, a := cleanupFixture(t)
	if _, err := f.service.HandleMessage(context.Background(), message("blocked")); err != nil {
		t.Fatal(err)
	}
	a.block = true
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := f.service.ProcessCleanups(ctx, 1); err == nil {
		t.Fatal("interrupted delete reported success")
	}
	var receipt honeypot.MessageCleanup
	if err := f.db.Where("message_discord_id = ?", "blocked").First(&receipt).Error; err != nil {
		t.Fatal(err)
	}
	if receipt.CompletedAt != nil || receipt.AttemptCount != 1 || !receipt.NextAttemptAt.After(time.Now()) {
		t.Fatal("interrupted delete lost its receipt", receipt)
	}
}
