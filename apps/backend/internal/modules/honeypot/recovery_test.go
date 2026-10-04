package honeypot_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// recoveryApplier adds the core's idempotency lookup and live recovery
// checks to a cleanupApplier. Like the core, applying a key twice returns
// the saved case.
type recoveryApplier struct {
	*cleanupApplier
	recoveryMu sync.Mutex
	saved      map[string]string
	prepareErr error
	lookupErr  error
	prepares   int
}

func (a *recoveryApplier) FindHoneypotCase(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	return honeypot.ApplyResult{CaseID: a.saved[request.IdempotencyKey]}, a.lookupErr
}

func (a *recoveryApplier) PrepareHoneypotRecovery(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyRequest, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	a.prepares++
	request.ContextURL = "https://discord.com/channels/guild/trap/" + request.ContextMessageDiscordID
	return request, a.prepareErr
}

func (a *recoveryApplier) ApplyHoneypotCase(ctx context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	a.recoveryMu.Lock()
	defer a.recoveryMu.Unlock()
	if id := a.saved[request.IdempotencyKey]; id != "" {
		return honeypot.ApplyResult{CaseID: id}, nil
	}
	result, err := a.cleanupApplier.ApplyHoneypotCase(ctx, request)
	if err == nil {
		a.saved[request.IdempotencyKey] = result.CaseID
	}
	return result, err
}

func recoveryFixture(t *testing.T) (*fixture, *recoveryApplier) {
	t.Helper()
	f, cleanup := cleanupFixture(t)
	a := &recoveryApplier{cleanupApplier: cleanup, saved: map[string]string{}}
	f.service = honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	return f, a
}

// interruptedIncident claims an incident as if Quack stopped right after.
func interruptedIncident(t *testing.T, f *fixture) (*honeypot.Trigger, honeypot.ApplyRequest) {
	t.Helper()
	settings, _, err := f.service.Settings(context.Background(), modules.Actor{GuildID: "guild-a", CanManage: true})
	if err != nil {
		t.Fatal(err)
	}
	event := message("interrupted")
	trigger, created, err := honeypot.NewStore(f.db).ClaimIncident(context.Background(), event, settings.TemplateID)
	if err != nil || !created {
		t.Fatal("claim", created, err)
	}
	request := honeypot.ApplyRequest{
		GuildID: trigger.GuildID, TemplateID: trigger.TemplateID, TargetDiscordUserID: trigger.TargetDiscordUserID,
		ContextChannelDiscordID: trigger.ChannelDiscordID, ContextMessageDiscordID: trigger.MessageDiscordID,
		ContextURL: event.MessageURL, IdempotencyKey: "honeypot:" + trigger.GuildID + ":" + trigger.MessageDiscordID,
		Source: honeypot.SourceHoneypot, ActorType: honeypot.ActorTypeSystem,
	}
	return trigger, request
}

// expireIncident ends the incident's lease without sleeping.
func expireIncident(t *testing.T, f *fixture, trigger *honeypot.Trigger) {
	t.Helper()
	if err := f.db.Model(&honeypot.Trigger{}).Where("id = ?", trigger.ID).
		Update("updated_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
}

// Recovery covers both crash windows: before the case was saved it opens
// it after live checks; after, it adopts it without any. Only then is the
// burst's bait deleted.
func TestIncidentRecoveryBeforeAndAfterCaseCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "before", true: "after"}[committed], func(t *testing.T) {
			f, a := recoveryFixture(t)
			trigger, request := interruptedIncident(t, f)
			if committed {
				if _, err := a.ApplyHoneypotCase(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				// The case exists, so recovery must adopt it even though the
				// member has left and the template is archived.
				a.prepareErr = errors.New("member no longer present")
				f.validator.templateErr = honeypot.ErrTemplateUnavailable
			}
			if _, err := f.service.HandleMessage(context.Background(), message("burst")); !errors.Is(err, honeypot.ErrDuplicate) {
				t.Fatal(err)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || a.deleted() != 0 {
				t.Fatal("cleanup before the incident finished", err)
			}
			if guilds, err := f.service.RecoverPending(context.Background(), 1); err != nil || len(guilds) != 0 {
				t.Fatal("a live lease was recovered", guilds, err)
			}
			expireIncident(t, f, trigger)
			guilds, err := f.service.RecoverPending(context.Background(), 1)
			if err != nil || len(guilds) != 1 {
				t.Fatal("recovery failed", guilds, err)
			}
			if a.count() != 1 || committed && a.prepares != 0 || !committed && a.prepares != 1 {
				t.Fatal("recovery repeated or skipped case work", a.count(), a.prepares)
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || a.deleted() != 2 {
				t.Fatal("cleanup did not resume", a.attempts, err)
			}
			rows, err := f.service.WarningRefreshes(context.Background(), time.Now().Add(time.Second))
			if err != nil || len(rows) != 1 {
				t.Fatal("recovery did not request a warning refresh", rows, err)
			}
		})
	}
}

// The lease admits one recovery across concurrent services.
func TestOverlappingIncidentRecoveryWorkers(t *testing.T) {
	f, a := recoveryFixture(t)
	trigger, _ := interruptedIncident(t, f)
	expireIncident(t, f, trigger)
	other := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			service := f.service
			if i%2 == 0 {
				service = other
			}
			if _, err := service.RecoverPending(context.Background(), 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if a.count() != 1 || a.prepares != 1 {
		t.Fatal("overlapping recovery repeated work", a.count(), a.prepares)
	}
}

// Without a saved case, recovery opens nothing when Quack lost permission,
// the template was archived, the author is now staff, the honeypot was
// switched off, or the lookup failed, and the bait stays as evidence.
func TestRecoveryRechecksPolicyBeforeNewCase(t *testing.T) {
	for _, scenario := range []string{"permission", "template", "staff", "disabled", "lookup unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			f, a := recoveryFixture(t)
			trigger, _ := interruptedIncident(t, f)
			expireIncident(t, f, trigger)
			actor := modules.Actor{GuildID: "guild-a", CanManage: true}
			switch scenario {
			case "permission":
				a.prepareErr = errors.New("bot permission lost")
			case "template":
				f.validator.templateErr = honeypot.ErrTemplateUnavailable
			case "staff":
				a.prepareErr = honeypot.ErrExempt
			case "lookup unavailable":
				a.lookupErr = errors.New("database unavailable")
			case "disabled":
				settings, _, _ := f.service.Settings(context.Background(), actor)
				if _, _, err := f.service.UpdateSettings(context.Background(), actor, false, settings); err != nil {
					t.Fatal(err)
				}
			}
			_, _ = f.service.RecoverPending(context.Background(), 1)
			if a.count() != 0 {
				t.Fatal("unsafe recovery applied moderation")
			}
			if err := f.service.ProcessCleanups(context.Background(), 25); err != nil || a.deleted() != 0 {
				t.Fatal("unsafe recovery deleted evidence", err)
			}
		})
	}
}

// A late burst message joins a pending incident, and recovery after a
// restart finishes it and releases both messages.
func TestRestartRecoversExpiredIncident(t *testing.T) {
	f, a := recoveryFixture(t)
	trigger, _ := interruptedIncident(t, f)
	if err := f.db.Model(&honeypot.Trigger{}).Where("id = ?", trigger.ID).
		Update("created_at", time.Now().UTC().Add(-2*time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.HandleMessage(context.Background(), message("late-burst")); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatal("the pending incident lost a late burst message", err)
	}
	expireIncident(t, f, trigger)
	restarted := honeypot.NewService(f.registry, honeypot.NewStore(f.db), f.audit, f.validator, f.validator, a)
	if _, err := restarted.RecoverPending(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if err := restarted.ProcessCleanups(context.Background(), 8); err != nil {
		t.Fatal(err)
	}
	var recovered honeypot.Trigger
	if err := f.db.First(&recovered).Error; err != nil {
		t.Fatal(err)
	}
	if recovered.Outcome != honeypot.OutcomeCreated || a.count() != 1 || a.deleted() != 2 {
		t.Fatal("restart repeated or lost the incident", recovered, a.count(), a.attempts)
	}
}
