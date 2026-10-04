package quack_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

type fakeAuditMirrorSender struct {
	mu       sync.Mutex
	messages []quack.AuditMirrorMessage
	err      error
}

func (f *fakeAuditMirrorSender) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	if f.err != nil {
		return "", f.err
	}
	return "message-" + message.AuditEntryID, nil
}

func (f *fakeAuditMirrorSender) sent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.messages)
}

// mirrorRow is the audit_mirror_deliveries row for one entry.
type mirrorRow struct {
	Status             string
	Attempts           int
	NextAttemptAt      time.Time
	LastError          string
	DeliveredMessageID string
}

func readMirrorRow(t *testing.T, repository *store.Store, entryID string) mirrorRow {
	t.Helper()
	var row mirrorRow
	if err := repository.DB().Table("audit_mirror_deliveries").Where("audit_entry_id = ?", entryID).Take(&row).Error; err != nil {
		t.Fatalf("read delivery for %s: %v", entryID, err)
	}
	return row
}

// makeDue moves every pending delivery's next attempt into the past.
func makeDue(t *testing.T, repository *store.Store) {
	t.Helper()
	if err := repository.DB().Table("audit_mirror_deliveries").Where("status = ?", quack.AuditMirrorPending).
		Update("next_attempt_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
}

func countAudit(t *testing.T, repository *store.Store, guildID string, action quack.AuditAction) int64 {
	t.Helper()
	page, err := repository.ListAuditLogEntriesFiltered(context.Background(),
		quack.ListAuditLogEntriesParams{GuildID: guildID, Action: string(action), Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	return page.Total
}

// newMirrorGuild bootstraps a guild and, when channel is set, points its
// audit mirror there. It drains whatever bootstrapping queued.
func newMirrorGuild(t *testing.T, repository *store.Store, discordID, channel string) string {
	t.Helper()
	ctx := context.Background()
	moderator := templateGuildContext(t, repository, discordID, "moderator", uint64(discordgo.PermissionModerateMembers))
	if _, err := repository.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(), DiscordGuildID: discordID, Name: "Guild", OwnerDiscordUserID: "owner-1"}); err != nil {
		t.Fatal(err)
	}
	if channel != "" {
		if err := repository.DB().Table("guild_settings").Where("guild_id = ?", moderator.Guild.ID).
			Update("audit_mirror_channel_discord_id", channel).Error; err != nil {
			t.Fatal(err)
		}
	}
	return moderator.Guild.ID
}

func addImportant(t *testing.T, repository *store.Store, guildID, resourceID string) quack.AuditLogEntry {
	t.Helper()
	entry := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionCaseVoid),
		ResourceType: "case", ResourceID: resourceID, Result: quack.AuditResultSuccess, MetadataJSON: "{}"}
	if err := repository.CreateAuditLogEntry(context.Background(), &entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func TestAuditMirrorIsNonBlockingRedactedAndRepairable(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	guildID := newMirrorGuild(t, repository, "mirror-guild", "123456789012345678")
	sender := &fakeAuditMirrorSender{}
	worker := quack.NewAuditMirror(repository, sender)

	entry := quack.AuditLogEntry{GuildID: guildID, ActorDiscordUserID: "moderator", Source: quack.AuditSourceDiscord, Action: string(quack.AuditActionCaseCreate), ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess, MetadataJSON: `{"token":"do-not-send","case_id":"case-1"}`}
	if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0].AuditEntryID != entry.ID || sender.messages[0].MetadataJSON != `{"case_id":"case-1","token":"[REDACTED]"}` {
		t.Fatalf("unexpected redacted mirror delivery: %+v", sender.messages)
	}
	if row := readMirrorRow(t, repository, entry.ID); row.Status != string(quack.AuditMirrorDelivered) || row.DeliveredMessageID != "message-"+entry.ID {
		t.Fatalf("delivery row = %+v", row)
	}
	if err := worker.PollOnce(ctx); err != nil || sender.sent() != 1 {
		t.Fatalf("delivered entry was mirrored more than once: count=%d err=%v", sender.sent(), err)
	}

	addImportant(t, repository, guildID, "case-concurrent")
	var polls sync.WaitGroup
	for range 20 {
		polls.Go(func() {
			if err := worker.PollOnce(ctx); err != nil {
				t.Errorf("concurrent poll: %v", err)
			}
		})
	}
	polls.Wait()
	if sender.sent() != 2 {
		t.Fatalf("concurrent polls duplicated mirror delivery: %+v", sender.messages)
	}

	second := addImportant(t, repository, guildID, "case-2")
	third := addImportant(t, repository, guildID, "case-3")
	sender.err = quack.ErrAuditMirrorChannelUnavailable
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	repaired, err := repository.GetGuildSettings(ctx, guildID)
	if err != nil || repaired.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("expected inaccessible mirror channel to be cleared, settings=%+v err=%v", repaired, err)
	}
	if sender.sent() != 3 {
		t.Fatalf("sent %d messages; the cleared channel should not be tried again", sender.sent())
	}
	for _, id := range []string{second.ID, third.ID} {
		if row := readMirrorRow(t, repository, id); row.Status != string(quack.AuditMirrorSkipped) {
			t.Errorf("delivery after the channel was cleared = %+v, want skipped", row)
		}
	}
	if repairs := countAudit(t, repository, guildID, quack.AuditActionMirrorRepaired); repairs != 1 {
		t.Fatalf("%d repair entries, want 1", repairs)
	}
	for _, action := range []quack.AuditAction{quack.AuditActionMirrorDelivered, quack.AuditActionMirrorSkipped, quack.AuditActionMirrorFailed} {
		if n := countAudit(t, repository, guildID, action); n != 0 {
			t.Errorf("%d %s audit entries; delivery state belongs in audit_mirror_deliveries", n, action)
		}
	}
}

func TestAuditMirrorQueuesNothingWithoutAChannel(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	guildID := newMirrorGuild(t, repository, "quiet-guild", "")
	addImportant(t, repository, guildID, "case-1")
	var queued int64
	if err := repository.DB().Table("audit_mirror_deliveries").Count(&queued).Error; err != nil || queued != 0 {
		t.Fatalf("%d deliveries queued without a channel (err %v)", queued, err)
	}
	sender := &fakeAuditMirrorSender{}
	if err := quack.NewAuditMirror(repository, sender).PollOnce(ctx); err != nil || sender.sent() != 0 {
		t.Fatalf("poll sent %d, %v", sender.sent(), err)
	}
	page, err := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: string(quack.AuditActionMirrorSkipped), Limit: 1})
	if err != nil || page.Total != 0 {
		t.Fatalf("skipped entries = %+v, %v; want none", page, err)
	}
}

func TestAuditMirrorBacksOffAndGivesUpOncePerOutage(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	guildID := newMirrorGuild(t, repository, "flaky-guild", "123456789012345678")
	sender := &fakeAuditMirrorSender{err: context.DeadlineExceeded}
	worker := quack.NewAuditMirror(repository, sender)

	first := addImportant(t, repository, guildID, "case-1")
	others := []quack.AuditLogEntry{addImportant(t, repository, guildID, "case-2"), addImportant(t, repository, guildID, "case-3")}
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// One failed send stands for the guild's whole batch.
	if sender.sent() != 1 {
		t.Fatalf("sent %d messages after the guild's first failure, want 1", sender.sent())
	}
	wantBackoff := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}
	for attempt, backoff := range wantBackoff {
		for _, entry := range append([]quack.AuditLogEntry{first}, others...) {
			row := readMirrorRow(t, repository, entry.ID)
			if row.Status != string(quack.AuditMirrorPending) || row.Attempts != attempt+1 || row.LastError != "delivery_failed" {
				t.Fatalf("after %d failures: %+v", attempt+1, row)
			}
			if wait := time.Until(row.NextAttemptAt); wait < backoff-time.Minute || wait > backoff {
				t.Fatalf("after %d failures the next attempt is %v away, want about %v", attempt+1, wait, backoff)
			}
		}
		sent := sender.sent()
		if err := worker.PollOnce(ctx); err != nil || sender.sent() != sent {
			t.Fatalf("poll during backoff sent %d more, %v", sender.sent()-sent, err)
		}
		makeDue(t, repository)
		if err := worker.PollOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range append([]quack.AuditLogEntry{first}, others...) {
		if row := readMirrorRow(t, repository, entry.ID); row.Status != string(quack.AuditMirrorFailed) || row.Attempts != 6 {
			t.Fatalf("after the last attempt: %+v, want failed", row)
		}
	}
	if n := countAudit(t, repository, guildID, quack.AuditActionMirrorFailed); n != 1 {
		t.Fatalf("%d give-up entries for one outage, want 1", n)
	}
	sent := sender.sent()
	makeDue(t, repository)
	if err := worker.PollOnce(ctx); err != nil || sender.sent() != sent {
		t.Fatalf("given-up deliveries were tried again: %d more, %v", sender.sent()-sent, err)
	}

	// Once delivery works again, a later outage is reported afresh.
	sender.err = nil
	addImportant(t, repository, guildID, "case-4")
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sender.err = context.DeadlineExceeded
	addImportant(t, repository, guildID, "case-5")
	for range wantBackoff {
		if err := worker.PollOnce(ctx); err != nil {
			t.Fatal(err)
		}
		makeDue(t, repository)
	}
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countAudit(t, repository, guildID, quack.AuditActionMirrorFailed); n != 2 {
		t.Fatalf("%d give-up entries after a second outage, want 2", n)
	}
}

// TestAuditMirrorNeverResendsAfterARestart covers a process that stopped
// mid-batch: a delivery it had started sending is never sent again, while
// one it had only claimed is sent once by the next process.
func TestAuditMirrorNeverResendsAfterARestart(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	guildID := newMirrorGuild(t, repository, "restart-guild", "123456789012345678")
	started := addImportant(t, repository, guildID, "case-1")
	claimedOnly := addImportant(t, repository, guildID, "case-2")

	claimed, err := repository.ClaimAuditMirrorDeliveries(ctx, 10)
	if err != nil || len(claimed) != 2 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if err := repository.BeginAuditMirrorDelivery(ctx, started.ID, claimed[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	// The first process dies here. Its leases run out.
	if err := repository.DB().Table("audit_mirror_deliveries").Where("lease_token = ?", claimed[0].LeaseToken).
		Update("lease_expires_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}

	sender := &fakeAuditMirrorSender{}
	worker := quack.NewAuditMirror(repository, sender)
	for range 2 {
		if err := worker.PollOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if sender.sent() != 1 || sender.messages[0].AuditEntryID != claimedOnly.ID {
		t.Fatalf("after restart sent %+v, want only the unsent claim", sender.messages)
	}
	if row := readMirrorRow(t, repository, started.ID); row.Status != string(quack.AuditMirrorFailed) || row.LastError != "delivery_outcome_unknown" {
		t.Fatalf("interrupted send = %+v, want failed with an unknown outcome", row)
	}
	if n := countAudit(t, repository, guildID, quack.AuditActionMirrorFailed); n != 0 {
		t.Fatalf("%d give-up entries for an interrupted send, want none", n)
	}
}
