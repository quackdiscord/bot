package honeypot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"gorm.io/gorm"
)

// A burst of messages from one member is one incident; a message after the
// burst window is a new one.
func TestMemberBurstCreatesOneIncident(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			_, err := fixture.service.HandleMessage(context.Background(), message(fmt.Sprintf("burst-%d", i)))
			if err != nil && !errors.Is(err, honeypot.ErrDuplicate) {
				t.Errorf("burst: %v", err)
			}
		})
	}
	wg.Wait()
	if fixture.applier.count() != 1 {
		t.Fatalf("burst opened %d cases", fixture.applier.count())
	}
	if err := fixture.db.Model(&honeypot.Trigger{}).Where("guild_id = ?", "guild-a").
		Update("created_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.HandleMessage(context.Background(), message("later")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 2 {
		t.Fatal("a later incident was suppressed")
	}
}

func TestFailedIncidentDoesNotSuppressNextMessage(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	fixture.applier.err = errors.New("case unavailable")
	if _, err := fixture.service.HandleMessage(context.Background(), message("failed")); err == nil {
		t.Fatal("expected a case failure")
	}
	fixture.applier.err = nil
	if _, err := fixture.service.HandleMessage(context.Background(), message("retry")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 2 {
		t.Fatal("the failed incident blocked the next message")
	}
}

// A gateway job whose settings read predates an admin edit must not claim
// an incident under the old settings.
func TestIncidentClaimRechecksChangedConfiguration(t *testing.T) {
	for _, test := range []struct {
		name     string
		enabled  bool
		settings honeypot.Settings
		want     error
	}{
		{"disabled", false, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"}, honeypot.ErrDisabled},
		{"new template", true, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "replacement"}, honeypot.ErrNotTrigger},
		{"new channel", true, honeypot.Settings{ChannelDiscordID: "replacement", TemplateID: "template"}, honeypot.ErrNotTrigger},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := setup(t)
			actor := enable(t, fixture, "guild-a")
			if _, _, err := fixture.service.UpdateSettings(context.Background(), actor, test.enabled, test.settings); err != nil {
				t.Fatal(err)
			}
			_, claimed, err := honeypot.NewStore(fixture.db).ClaimIncident(context.Background(), message("stale-job"), "template")
			if claimed || !errors.Is(err, test.want) {
				t.Fatalf("stale claim = %v, %v; want %v", claimed, err, test.want)
			}
			var count int64
			if err := fixture.db.Model(&honeypot.Trigger{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("stale settings stored %d incidents (%v)", count, err)
			}
		})
	}
}

// A database outage while checking the template fails the incident but
// leaves the honeypot on, so it recovers without an admin.
func TestTemporaryTemplateFailureKeepsHoneypotEnabled(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	fixture.validator.templateErr = errors.New("temporary database failure")
	if _, err := fixture.service.HandleMessage(context.Background(), message("outage")); err == nil {
		t.Fatal("lookup failure ignored")
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || !status.Enabled || status.Statistics.Failed != 1 {
		t.Fatalf("temporary failure turned the trap off: %+v %v", status, err)
	}
	fixture.validator.templateErr = nil
	if _, err := fixture.service.HandleMessage(context.Background(), message("recovered")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 1 {
		t.Fatal("trap did not recover on its own")
	}
}

// Role exemptions saved before the rewrite no longer protect anyone.
func TestObsoleteRoleExemptionsDoNotBypassTrap(t *testing.T) {
	fixture := setup(t)
	if _, err := fixture.registry.SetConfiguration(context.Background(), modules.Configuration{
		GuildID: "guild-a", ModuleID: modules.Honeypots, Enabled: true,
		ConfigJSON: `{"channel_discord_id":"trap","template_id":"template","exempt_role_discord_ids":["trusted"]}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.service.HandleMessage(context.Background(), message("ordinary-member")); err != nil {
		t.Fatal(err)
	}
	if fixture.applier.count() != 1 {
		t.Fatal("an obsolete role list bypassed moderation")
	}
}

// Ordinary bots are caught; that is most of what a trap is for.
func TestOrdinaryBotTriggersHoneypot(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	event := message("ordinary-bot")
	event.IsBot = true
	if result, err := fixture.service.HandleMessage(context.Background(), event); err != nil || result.CaseID == "" || fixture.applier.count() != 1 {
		t.Fatalf("ordinary bot bypassed the trap: %+v %v", result, err)
	}
}

// Recording a replacement warning changes only its ID, and never over an
// admin's concurrent edit.
func TestWarningReplacementPreservesCurrentConfiguration(t *testing.T) {
	fixture := setup(t)
	ctx := context.Background()
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	original := honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Custom warning", WarningMessageID: "old"}
	if _, _, err := fixture.service.UpdateSettings(ctx, actor, true, original); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RecordWarningReplacement(ctx, actor.GuildID, original, "new"); err != nil {
		t.Fatal(err)
	}
	saved, _, err := fixture.service.Settings(ctx, actor)
	if err != nil || saved.WarningMessageID != "new" || saved.WarningText != original.WarningText || saved.TemplateID != original.TemplateID {
		t.Fatalf("replacement changed policy: %+v %v", saved, err)
	}
	if err := fixture.service.RecordWarningReplacement(ctx, actor.GuildID, original, "stale"); err == nil {
		t.Fatal("a stale replacement overwrote the warning")
	}
	changed := saved
	changed.WarningText = "Administrator edit"
	if _, _, err := fixture.service.UpdateSettings(ctx, actor, true, changed); err != nil {
		t.Fatal(err)
	}
	if err := fixture.service.RecordWarningReplacement(ctx, actor.GuildID, saved, "stale"); err == nil {
		t.Fatal("a replacement overwrote a concurrent warning edit")
	}
}

// Any number of refresh requests is one row; a request made during a
// refresh survives it; the send fence holds until released.
func TestWarningRefreshCoalescing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	enable(t, f, "guild-a")
	for range 10 {
		if err := f.service.RequestWarningRefresh(ctx, "guild-a"); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	old := rows[0]
	if err := f.service.RequestWarningRefresh(ctx, "guild-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CompleteWarningRefresh(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	rows, err = f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 || rows[0].Revision == old.Revision {
		t.Fatal("a newer request was lost", rows, err)
	}
	settings := honeypot.Settings{ChannelDiscordID: "trap", WarningMessageID: "old"}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); !errors.Is(err, honeypot.ErrWarningDeliveryUnknown) {
		t.Fatalf("second reservation: %v", err)
	}
	if err := f.service.ReleaseWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReserveWarningSend(ctx, "guild-a", settings); err != nil {
		t.Fatal(err)
	}
	if err := f.service.CompleteWarningRefresh(ctx, rows[0], false); err != nil {
		t.Fatal(err)
	}
	if rows, err = f.service.WarningRefreshes(ctx, time.Now().Add(time.Second)); err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
}

// A saved case requests a warning refresh; failed refreshes never repeat
// the moderation.
func TestPresentationRequestsDoNotRepeatModeration(t *testing.T) {
	f := setup(t)
	enable(t, f, "guild-a")
	pool := honeypot.NewPool(f.service)
	pool.Start(context.Background())
	for range 2 {
		pool.Submit(message("same"))
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.applier.count() != 1 {
		t.Fatal("moderation repeated", f.applier.count())
	}
	rows, err := f.service.WarningRefreshes(context.Background(), time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	for range 3 {
		if err := f.service.CompleteWarningRefresh(context.Background(), rows[0], true); err != nil {
			t.Fatal(err)
		}
	}
	if f.applier.count() != 1 {
		t.Fatal("presentation retried moderation")
	}
}

// An incident's outcome and its warning refresh commit together.
func TestIncidentCompletionAndWarningRequestAreAtomic(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	store := honeypot.NewStore(f.db)
	trigger, _, err := store.Claim(ctx, message("atomic"), "template", honeypot.OutcomePending)
	if err != nil {
		t.Fatal(err)
	}
	const callback = "test:fail_warning_request"
	if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "honeypot_warning_refreshes" {
			tx.AddError(errors.New("warning storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); err == nil {
		t.Fatal("partial completion succeeded")
	}
	if err := f.db.Callback().Create().Remove(callback); err != nil {
		t.Fatal(err)
	}
	var saved honeypot.Trigger
	if err := f.db.First(&saved, "id = ?", trigger.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.Outcome != honeypot.OutcomePending || saved.CaseID != "" {
		t.Fatal("completion was not rolled back", saved)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatal(err)
	}
	next, err := f.service.WarningRefreshes(ctx, time.Now().Add(time.Second))
	if err != nil || len(next) != 1 || next[0].Revision != rows[0].Revision {
		t.Fatal("a duplicate completion requested another refresh", next, err)
	}
}
