package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

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

func TestStaffRolesRoundTripAndListInOneQuery(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	for _, id := range []string{"configured", "unconfigured"} {
		if _, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
			Starter: quack.StarterTemplate(), DiscordGuildID: id, Name: id, OwnerDiscordUserID: "owner",
		}); err != nil {
			t.Fatal(err)
		}
	}
	guild, err := s.GetGuildByDiscordID(ctx, "configured")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.GetGuildSettings(ctx, guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.ModeratorRoleIDs == nil || len(settings.ModeratorRoleIDs) != 0 {
		t.Fatalf("unset moderator roles = %#v, want an empty list", settings.ModeratorRoleIDs)
	}
	settings.ModeratorRoleIDs = []string{"11", "12"}
	settings.RulesManagerRoleIDs = []string{"13"}
	if _, err := s.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
		t.Fatal(err)
	}
	stored, err := s.GetGuildSettings(ctx, guild.ID)
	if err != nil || !slices.Equal(stored.ModeratorRoleIDs, []string{"11", "12"}) || !slices.Equal(stored.RulesManagerRoleIDs, []string{"13"}) {
		t.Fatalf("stored = %+v, %v", stored, err)
	}

	listed, err := s.ListGuildStaffRoles(ctx, []string{"configured", "unconfigured", "unknown"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || !slices.Equal(listed["configured"].ModeratorRoleIDs, []string{"11", "12"}) ||
		!slices.Equal(listed["configured"].RulesManagerRoleIDs, []string{"13"}) || len(listed["unconfigured"].ModeratorRoleIDs) != 0 {
		t.Fatalf("listed = %+v", listed)
	}
	if empty, err := s.ListGuildStaffRoles(ctx, nil); err != nil || len(empty) != 0 {
		t.Fatalf("listing no guilds = %+v, %v", empty, err)
	}
}

func TestDiscordUserMFA(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	if enabled, err := s.DiscordUserMFAEnabled(ctx, "user"); err != nil || enabled {
		t.Fatalf("never signed in = %v, %v; want false", enabled, err)
	}
	for _, want := range []bool{true, false, true} {
		if err := s.RecordDiscordUserMFA(ctx, "user", want, time.Now()); err != nil {
			t.Fatal(err)
		}
		if enabled, err := s.DiscordUserMFAEnabled(ctx, "user"); err != nil || enabled != want {
			t.Fatalf("after recording %v = %v, %v", want, enabled, err)
		}
	}
	if enabled, err := s.DiscordUserMFAEnabled(ctx, "other"); err != nil || enabled {
		t.Fatalf("another user = %v, %v; want false", enabled, err)
	}
}
