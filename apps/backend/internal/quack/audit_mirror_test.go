package quack_test

import (
	"context"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

type fakeAuditMirrorSender struct {
	mu       sync.Mutex
	messages []quack.AuditMirrorMessage
	err      error
}

func (f *fakeAuditMirrorSender) SendAuditMirror(ctx context.Context, message quack.AuditMirrorMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages = append(f.messages, message)
	return f.err
}

func TestAuditMirrorIsNonBlockingRedactedAndRepairable(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	moderator := templateGuildContext(t, repository, "mirror-guild", "moderator", uint64(discordgo.PermissionModerateMembers))
	if _, err := repository.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(), DiscordGuildID: "mirror-guild", Name: "Guild", OwnerDiscordUserID: "owner-1"}); err != nil {
		t.Fatal(err)
	}
	settings, err := repository.GetGuildSettings(ctx, moderator.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings.AuditMirrorChannelDiscordID = "123456789012345678"
	if err := repository.DB().Model(&quack.GuildSettings{}).Where("id = ?", settings.ID).Update("audit_mirror_channel_discord_id", settings.AuditMirrorChannelDiscordID).Error; err != nil {
		t.Fatal(err)
	}
	sender := &fakeAuditMirrorSender{}
	worker := quack.NewAuditMirror(repository, sender)
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	sender.mu.Lock()
	sender.messages = nil
	sender.mu.Unlock()
	entry := quack.AuditLogEntry{GuildID: moderator.Guild.ID, ActorDiscordUserID: "moderator", Source: quack.AuditSourceDiscord, Action: string(quack.AuditActionCaseCreate), ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess, MetadataJSON: `{"token":"do-not-send","case_id":"case-1"}`}
	if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != 1 || sender.messages[0].AuditEntryID != entry.ID || sender.messages[0].MetadataJSON != `{"case_id":"case-1","token":"[REDACTED]"}` {
		t.Fatalf("unexpected redacted mirror delivery: %+v", sender.messages)
	}
	if err := worker.PollOnce(ctx); err != nil || len(sender.messages) != 1 {
		t.Fatalf("delivered entry was mirrored more than once: count=%d err=%v", len(sender.messages), err)
	}
	concurrent := quack.AuditLogEntry{GuildID: moderator.Guild.ID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionCaseVoid), ResourceType: "case", ResourceID: "case-concurrent", Result: quack.AuditResultSuccess, MetadataJSON: "{}"}
	if err := repository.CreateAuditLogEntry(ctx, &concurrent); err != nil {
		t.Fatal(err)
	}
	var polls sync.WaitGroup
	for range 20 {
		polls.Add(1)
		go func() {
			defer polls.Done()
			if err := worker.PollOnce(ctx); err != nil {
				t.Errorf("concurrent poll: %v", err)
			}
		}()
	}
	polls.Wait()
	if len(sender.messages) != 2 {
		t.Fatalf("concurrent polls duplicated mirror delivery: %+v", sender.messages)
	}

	second := quack.AuditLogEntry{GuildID: moderator.Guild.ID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionCaseVoid), ResourceType: "case", ResourceID: "case-2", Result: quack.AuditResultSuccess, MetadataJSON: "{}"}
	if err := repository.CreateAuditLogEntry(ctx, &second); err != nil {
		t.Fatal(err)
	}
	sender.err = quack.ErrAuditMirrorChannelUnavailable
	if err := worker.PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	repaired, err := repository.GetGuildSettings(ctx, moderator.Guild.ID)
	if err != nil || repaired.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("expected inaccessible mirror channel to be cleared, settings=%+v err=%v", repaired, err)
	}
	failures, _ := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: string(quack.AuditActionMirrorFailed), Limit: 10})
	repairs, _ := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: string(quack.AuditActionMirrorRepaired), Limit: 10})
	if failures.Total != 1 || repairs.Total != 1 {
		t.Fatalf("expected durable mirror failure and repair history, failures=%+v repairs=%+v", failures, repairs)
	}
}
