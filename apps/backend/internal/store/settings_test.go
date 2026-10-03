package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestGuildSettingsUpdateAndChannelRepair(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	bootstrap, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild", Name: "Guild", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	guildID := bootstrap.Guild.ID
	settings := bootstrap.Settings
	settings.AuditMirrorChannelDiscordID = "shared-channel"
	settings.ManagedEvidenceChannelDiscordID = "shared-channel"
	settings.AppealQueueChannelDiscordID = "shared-channel"
	settings.AppealRejoinURL = "https://discord.gg/quack"
	settings.AppealReviewReasonRequired = true
	settings.StarterPolicyTemplateID = "ignored"
	updated, err := s.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{
		Settings: settings,
		Audit: &quack.AuditLogEntry{
			GuildID:      guildID,
			Source:       quack.AuditSourceAPI,
			Action:       "guild_settings.update",
			ResourceType: "guild_settings",
			Result:       quack.AuditResultSuccess,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AuditMirrorChannelDiscordID != "shared-channel" || updated.AppealQueueChannelDiscordID != "shared-channel" ||
		updated.AppealRejoinURL != "https://discord.gg/quack" || !updated.AppealReviewReasonRequired {
		t.Fatalf("update = %+v", updated)
	}
	if updated.StarterPolicyTemplateID != bootstrap.Settings.StarterPolicyTemplateID {
		t.Fatalf("update rebound the starter template to %q", updated.StarterPolicyTemplateID)
	}

	repair := &quack.AuditLogEntry{Source: quack.AuditSourceDiscord, Action: "guild_settings.channel_reference.cleared",
		ResourceType: "guild_settings", Result: quack.AuditResultSuccess}
	if _, err := s.ClearGuildChannelReferences(ctx, guildID, "other-channel", repair); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.ClearGuildChannelReferences(ctx, guildID, "shared-channel", repair)
	if err != nil || cleared.AuditMirrorChannelDiscordID != "" || cleared.ManagedEvidenceChannelDiscordID != "" ||
		cleared.AppealQueueChannelDiscordID != "" || cleared.AppealRejoinURL == "" {
		t.Fatalf("clear = %+v, %v", cleared, err)
	}
	audits, err := s.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: repair.Action})
	if err != nil || audits.Total != 1 {
		t.Fatalf("repair audits = %+v, %v; want only the clear that changed something", audits, err)
	}

	missing := quack.UpdateGuildSettingsParams{Settings: quack.GuildSettings{GuildID: "unknown"}}
	if _, err := s.UpdateGuildSettings(ctx, missing); !errors.Is(err, quack.ErrGuildSettingsNotFound) {
		t.Fatalf("update for unknown guild = %v, want ErrGuildSettingsNotFound", err)
	}
	if err := notFound(s.GetGuildSettings(ctx, "unknown")); err != nil {
		t.Errorf("GetGuildSettings(unknown): %v", err)
	}
}

func TestSetManagedEvidenceChannelKeepsConcurrentChanges(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	bootstrap, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild", Name: "Guild", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	guildID := bootstrap.Guild.ID
	audit := &quack.AuditLogEntry{Source: quack.AuditSourceSystem, Action: "evidence_channel.ensure",
		ResourceType: "guild_settings", Result: quack.AuditResultSuccess}
	if got, err := s.SetManagedEvidenceChannel(ctx, guildID, "", "first", audit); err != nil || got != "first" {
		t.Fatalf("first set = %q, %v", got, err)
	}
	if got, err := s.SetManagedEvidenceChannel(ctx, guildID, "", "second", audit); err != nil || got != "first" {
		t.Fatalf("stale set = %q, %v; want the concurrent winner", got, err)
	}
	settings, err := s.GetGuildSettings(ctx, guildID)
	if err != nil || settings.ManagedEvidenceChannelDiscordID != "first" {
		t.Fatalf("settings = %+v, %v", settings, err)
	}
	if _, err := s.SetManagedEvidenceChannel(ctx, guildID, "first", "", audit); err == nil {
		t.Fatal("set an empty evidence channel")
	}
}

func TestGuildAppealSettingsUpsert(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	if err := notFound(s.GetGuildAppealSettings(ctx, guildID)); err != nil {
		t.Fatalf("settings before configuration: %v", err)
	}
	audit := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "appeal_settings.update",
		ResourceType: "guild_appeal_settings", Result: quack.AuditResultSuccess}
	first, err := s.UpdateGuildAppealSettings(ctx, quack.UpdateGuildAppealSettingsParams{
		Settings: quack.GuildAppealSettings{GuildID: guildID, QuestionsJSON: `[{"id":"a"}]`, UpdatedByDiscordUserID: "one"},
		Audit:    audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.UpdateGuildAppealSettings(ctx, quack.UpdateGuildAppealSettingsParams{
		Settings: quack.GuildAppealSettings{GuildID: guildID, QuestionsJSON: `[{"id":"b"}]`, UpdatedByDiscordUserID: "two"},
		Audit:    audit,
	})
	if err != nil || second.ID != first.ID || second.QuestionsJSON != `[{"id":"b"}]` || second.UpdatedByDiscordUserID != "two" {
		t.Fatalf("second update = %+v, %v", second, err)
	}
	if got, err := s.GetGuildAppealSettings(ctx, guildID); err != nil || got.QuestionsJSON != second.QuestionsJSON {
		t.Fatalf("GetGuildAppealSettings = %+v, %v", got, err)
	}
}
