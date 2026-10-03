package quack_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestRedactAuditMetadataRecursesAcrossCredentialsContentAndPayloads(t *testing.T) {
	raw := `{"safe":"count","nested":{"oauth_token":"oauth-secret","cookie":"cookie-secret","session_id":"session-secret","webhook_url":"https://discord.invalid/webhook-secret","member_content":"private words","action_payload":{"reason":"private payload"}},"items":[{"authorization":"Bearer secret"}]}`
	redacted := quack.RedactAuditMetadata(raw)
	for _, secret := range []string{"oauth-secret", "cookie-secret", "session-secret", "webhook-secret", "private words", "private payload", "Bearer secret"} {
		if strings.Contains(redacted, secret) {
			t.Fatalf("redaction exposed %q in %s", secret, redacted)
		}
	}
	if !strings.Contains(redacted, `"safe":"count"`) {
		t.Fatalf("redaction removed safe aggregate field: %s", redacted)
	}
}

func TestAuditServiceListPermissionsAndFilters(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionAdministrator))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	entries := []quack.AuditLogEntry{
		{GuildID: adminContext.Guild.ID, ActorDiscordUserID: "actor-1", ActorPermissionBits: uint64(discordgo.PermissionManageGuild), Source: quack.AuditSourceAPI, Action: "case.create", ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess, MetadataJSON: "{}"},
		{GuildID: adminContext.Guild.ID, ActorDiscordUserID: "actor-2", Source: quack.AuditSourceSystem, Action: "case_action.failed", ResourceType: "case_action_execution", ResourceID: "action-1", Result: quack.AuditResultFailure, MetadataJSON: "{}"},
	}
	for i := range entries {
		if err := store.CreateAuditLogEntry(ctx, &entries[i]); err != nil {
			t.Fatalf("create audit %d: %v", i, err)
		}
	}

	service := quack.NewAuditService(store)
	moderatorList, err := service.List(ctx, modContext, quack.AuditListInput{})
	if err != nil || moderatorList.Total != 2 {
		t.Fatalf("expected moderator complete audit access, list=%+v err=%v", moderatorList, err)
	}

	list, err := service.List(ctx, adminContext, quack.AuditListInput{Result: string(quack.AuditResultFailure), Limit: "10"})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if list.Total != 1 || len(list.Entries) != 1 || list.Entries[0].Action != "case_action.failed" {
		t.Fatalf("unexpected audit list: %+v", list)
	}

	_, err = service.List(ctx, adminContext, quack.AuditListInput{Result: "partial"})
	if !errors.Is(err, quack.ErrAuditValidation) {
		t.Fatalf("expected audit validation error, got %v", err)
	}
}

func TestAuditServiceRedactsAndFiltersCompleteContract(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "request-1", "trace-1")
	repository := newMigratedStore(t)
	moderator := templateGuildContext(t, repository, "audit-guild", "moderator", uint64(discordgo.PermissionModerateMembers))
	now := time.Now().UTC()
	entry := quack.AuditLogEntry{ULIDModel: quack.ULIDModel{CreatedAt: now.Add(-time.Minute)}, GuildID: moderator.Guild.ID, ActorDiscordUserID: "actor", Source: quack.AuditSourceHoneypot, Action: string(quack.AuditActionHoneypotTrigger), ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess, MetadataJSON: `{"case_id":"case-1","target_discord_user_id":"member-1","token":"secret","nested":{"request_payload":{"content":"private"}}}`}
	if err := repository.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	second := quack.AuditLogEntry{GuildID: moderator.Guild.ID, ActorDiscordUserID: "actor", Source: quack.AuditSourceHoneypot, Action: string(quack.AuditActionHoneypotTrigger), ResourceType: "case", ResourceID: "case-2", Result: quack.AuditResultSuccess, MetadataJSON: `{"case_id":"case-2","target_discord_user_id":"member-2"}`}
	if err := repository.CreateAuditLogEntry(ctx, &second); err != nil {
		t.Fatal(err)
	}
	seeded, _ := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Limit: 100})

	service := quack.NewAuditService(repository)
	result, err := service.List(ctx, moderator, quack.AuditListInput{Source: string(quack.AuditSourceHoneypot), CaseID: "case-1", MemberDiscordUserID: "member-1", CreatedAfter: now.Add(-time.Hour).Format(time.RFC3339), CreatedBefore: now.Add(time.Hour).Format(time.RFC3339), ReadSource: quack.AuditSourceDiscord})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || len(result.Entries) != 1 || result.Entries[0].CorrelationID != "" {
		// The seeded entry predates the trace; the read audit below owns trace-1.
		if result.Total != 1 || len(result.Entries) != 1 {
			t.Fatalf("unexpected filtered result: %+v seeded=%+v", result, seeded)
		}
	}
	metadata := fmt.Sprint(result.Entries[0].Metadata)
	if strings.Contains(metadata, "secret") || !strings.Contains(metadata, quack.AuditMetadataRedactedValue) {
		t.Fatalf("metadata was not recursively redacted: %s", metadata)
	}
	firstPage, err := service.List(ctx, moderator, quack.AuditListInput{Action: string(quack.AuditActionHoneypotTrigger), Limit: "1"})
	if err != nil || len(firstPage.Entries) != 1 || firstPage.NextCursor == "" {
		t.Fatalf("missing stable first audit page: %+v err=%v", firstPage, err)
	}
	secondPage, err := service.List(ctx, moderator, quack.AuditListInput{Action: string(quack.AuditActionHoneypotTrigger), Limit: "1", BeforeID: firstPage.NextCursor})
	if err != nil || len(secondPage.Entries) != 1 || secondPage.Entries[0].ID == firstPage.Entries[0].ID {
		t.Fatalf("cursor repeated or skipped page: first=%+v second=%+v err=%v", firstPage, secondPage, err)
	}
	audits, err := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, Action: string(quack.AuditActionAuditRead), Limit: 10})
	foundDiscordRead := false
	if err == nil {
		for _, audit := range audits.Entries {
			if audit.Source == quack.AuditSourceDiscord && audit.RequestID == "request-1" && audit.CorrelationID == "trace-1" {
				foundDiscordRead = true
			}
		}
	}
	if !foundDiscordRead {
		t.Fatalf("missing trace-linked Discord read audit: %+v err=%v", audits, err)
	}

	ordinary := templateGuildContext(t, repository, "audit-guild", "ordinary", uint64(discordgo.PermissionSendMessages))
	if _, err := service.List(ctx, ordinary, quack.AuditListInput{ReadSource: quack.AuditSourceDiscord}); !errors.Is(err, quack.ErrAuditPermissionDenied) {
		t.Fatalf("expected moderator permission denial, got %v", err)
	}
}
