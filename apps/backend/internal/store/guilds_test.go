package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestUpsertGuildAndStaff(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	guild, err := s.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "100", Name: "Before", OwnerDiscordUserID: "200"})
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := s.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "100", Name: "After", OwnerDiscordUserID: "200"})
	if err != nil || renamed.ID != guild.ID || renamed.Name != "After" || !renamed.IsActive {
		t.Fatalf("refresh guild = %+v, %v", renamed, err)
	}
	if err := notFound(s.GetGuildByDiscordID(ctx, "missing")); err != nil {
		t.Errorf("GetGuildByDiscordID(missing): %v", err)
	}
	if err := notFound(s.GetStaffMember(ctx, guild.ID, "missing")); err != nil {
		t.Errorf("GetStaffMember(missing): %v", err)
	}

	staff, err := s.UpsertStaffMember(ctx, quack.UpsertStaffMemberParams{
		GuildID: guild.ID, DiscordUserID: "300", LastSeenPermissionBits: 1, LastKnownDisplayName: "Old Name",
	})
	if err != nil || staff.LastActiveAt == nil {
		t.Fatalf("create staff = %+v, %v", staff, err)
	}
	updated, err := s.UpsertStaffMember(ctx, quack.UpsertStaffMemberParams{
		GuildID: guild.ID, DiscordUserID: "300", LastSeenPermissionBits: 64, LastKnownDisplayName: "New Name",
	})
	if err != nil || updated.ID != staff.ID || updated.LastSeenPermissionBits != 64 || updated.LastKnownDisplayName != "New Name" {
		t.Fatalf("refresh staff = %+v, %v", updated, err)
	}
	if got, err := s.GetStaffMember(ctx, guild.ID, "300"); err != nil || got.ID != staff.ID {
		t.Fatalf("GetStaffMember = %+v, %v", got, err)
	}
}

func TestBootstrapCreatesStarterOnce(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	input := quack.BootstrapGuildParams{
		Starter:        quack.StarterTemplate(),
		DiscordGuildID: "guild-bootstrap", Name: "Bootstrap", IconURL: "icon-url",
		OwnerDiscordUserID: "owner-1", KnownChannelDiscordIDs: []string{"channel-1"},
	}
	first, err := s.BootstrapGuild(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !first.GuildCreated || !first.StarterTemplateCreated || !first.Guild.IsActive {
		t.Fatalf("first bootstrap flags: %+v", first)
	}
	if first.Settings.StarterPolicyTemplateID != first.StarterTemplate.Template.ID || !first.Settings.StarterPolicyNoticePending {
		t.Fatalf("starter not bound to settings: %+v", first.Settings)
	}
	if !quack.IsStarterTemplate(first.StarterTemplate) {
		t.Fatalf("stored starter differs from the starter policy: %+v", first.StarterTemplate)
	}

	second, err := s.BootstrapGuild(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if second.GuildCreated || second.StarterTemplateCreated || second.Guild.ID != first.Guild.ID ||
		second.Settings.ID != first.Settings.ID || second.StarterTemplate.Template.ID != first.StarterTemplate.Template.ID {
		t.Fatalf("second bootstrap duplicated state: first=%+v second=%+v", first, second)
	}
	templates, err := s.ListCaseTemplates(ctx, first.Guild.ID)
	if err != nil || len(templates) != 1 {
		t.Fatalf("templates after two bootstraps = %d, %v; want 1", len(templates), err)
	}
}

func TestGuildLeaveAndRejoinKeepsSettings(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	bootstrap, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild", Name: "Before", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	auditChannel, evidenceChannel, introduction := "audit-channel", "evidence-channel", "Welcome"
	if _, err := s.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{GuildID: bootstrap.Guild.ID, Patch: quack.GuildSettingsPatch{
		AuditMirrorChannelDiscordID: &auditChannel, ManagedEvidenceChannelDiscordID: &evidenceChannel, NotificationIntroduction: &introduction,
	}}); err != nil {
		t.Fatal(err)
	}

	left, err := s.DeactivateGuild(ctx, "guild", &quack.AuditLogEntry{
		Source: quack.AuditSourceDiscord, Action: "guild.lifecycle.leave", ResourceType: "guild", Result: quack.AuditResultSuccess,
	})
	if err != nil || left.IsActive {
		t.Fatalf("deactivate = %+v, %v", left, err)
	}
	if err := notFound(s.DeactivateGuild(ctx, "unknown", nil)); err != nil {
		t.Errorf("DeactivateGuild(unknown): %v", err)
	}

	rejoined, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild", Name: "After", OwnerDiscordUserID: "owner-2",
		KnownChannelDiscordIDs: []string{"audit-channel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rejoined.Guild.ID != bootstrap.Guild.ID || !rejoined.Guild.IsActive ||
		rejoined.Guild.Name != "After" || rejoined.StarterTemplateCreated {
		t.Fatalf("rejoin did not reuse the guild: %+v", rejoined)
	}
	if rejoined.Settings.AuditMirrorChannelDiscordID != "audit-channel" || rejoined.Settings.ManagedEvidenceChannelDiscordID != "" {
		t.Fatalf("rejoin channel repair: %+v", rejoined.Settings)
	}
	if rejoined.Settings.NotificationIntroduction != "Welcome" {
		t.Fatalf("rejoin lost settings: %+v", rejoined.Settings)
	}
	audits, err := s.ListAuditLogEntries(ctx, bootstrap.Guild.ID)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]int{}
	for _, entry := range audits {
		actions[entry.Action]++
	}
	if actions["guild.lifecycle.bootstrap"] != 2 || actions["guild.lifecycle.leave"] != 1 ||
		actions["guild_settings.channel_references.repaired"] != 1 {
		t.Fatalf("lifecycle audits = %v", actions)
	}
}
