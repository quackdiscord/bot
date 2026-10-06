package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestGuildSettingsServiceAuthorizationAuditAndNotice(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	bootstrap, err := repositories.BootstrapGuild(ctx, quack.BootstrapGuildParams{Starter: quack.StarterTemplate(),
		DiscordGuildID: "settings-guild", Name: "Settings Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	manager := templateGuildContext(t, repositories, "settings-guild", "manager-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, repositories, "settings-guild", "moderator-1", uint64(discordgo.PermissionModerateMembers))
	registry := modules.NewRegistry(repositories.DB())
	service := quack.NewGuildSettingsService(repositories, allowStaffChannel{}, registry)

	auditChannel := "100000000000000001"
	intro, footer := "Welcome to this guild", "Review case details in Quack"
	tickets, logging, honeypot := true, true, false
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{TicketsEnabled: &tickets}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("switched on a module that was never set up: %v", err)
	}
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: bootstrap.Guild.ID, ModuleID: id}); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{
		AuditMirrorChannelDiscordID: &auditChannel,
		NotificationIntroduction:    &intro, NotificationFooter: &footer,
		TicketsEnabled: &tickets, GeneralLoggingEnabled: &logging, HoneypotEnabled: &honeypot,
	})
	if err != nil {
		t.Fatalf("update guild settings: %v", err)
	}
	if updated.AuditMirrorChannelDiscordID != auditChannel || updated.ManagedEvidenceChannelDiscordID != "" || !updated.TicketsEnabled || !updated.GeneralLoggingEnabled || updated.HoneypotEnabled {
		t.Fatalf("unexpected settings response: %+v", updated)
	}
	if states, err := registry.ModuleStates(ctx, bootstrap.Guild.ID); err != nil || states != (quack.ModuleStates{Tickets: true, GeneralLogging: true}) {
		t.Fatalf("module switches = %+v, %v", states, err)
	}
	noModules := quack.NewGuildSettingsService(repositories, allowStaffChannel{}, nil)
	if _, err := noModules.Update(ctx, manager, quack.GuildSettingsInput{HoneypotEnabled: &tickets}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("switched a module on without modules: %v", err)
	}

	evidenceChannel, noEvidence := "100000000000000002", ""
	if updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &evidenceChannel}); err != nil || updated.ManagedEvidenceChannelDiscordID != evidenceChannel {
		t.Fatalf("set evidence channel = %+v, %v", updated, err)
	}
	if updated, err := service.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &noEvidence}); err != nil || updated.ManagedEvidenceChannelDiscordID != "" {
		t.Fatalf("clear evidence channel = %+v, %v", updated, err)
	}
	unvalidated := quack.NewGuildSettingsService(repositories, nil, registry)
	if _, err := unvalidated.Update(ctx, manager, quack.GuildSettingsInput{ManagedEvidenceChannelDiscordID: &evidenceChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated evidence destination accepted: %v", err)
	}
	if _, err := unvalidated.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &auditChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("unvalidated audit destination accepted: %v", err)
	}
	deniedValue := "forbidden"
	if _, err := service.Update(ctx, moderator, quack.GuildSettingsInput{NotificationFooter: &deniedValue}); !errors.Is(err, quack.ErrGuildSettingsPermissionDenied) {
		t.Fatalf("expected denied moderator write, got %v", err)
	}
	tooLong := strings.Repeat("x", 2001)
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{NotificationIntroduction: &tooLong}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected validation failure, got %v", err)
	}
	invalidChannel := "not-a-channel"
	if _, err := service.Update(ctx, manager, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &invalidChannel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("expected non-snowflake channel rejection, got %v", err)
	}

	audits, err := repositories.ListAuditLogEntries(ctx, bootstrap.Guild.ID)
	if err != nil {
		t.Fatalf("list settings audits: %v", err)
	}
	results := map[quack.AuditResult]bool{}
	for _, audit := range audits {
		if audit.Action == "guild_settings.update" {
			results[audit.Result] = true
		}
	}
	for _, result := range []quack.AuditResult{quack.AuditResultSuccess, quack.AuditResultFailure, quack.AuditResultDenied} {
		if !results[result] {
			t.Fatalf("missing %s settings audit in %+v", result, audits)
		}
	}

	acknowledged, err := service.AcknowledgeStarterPolicyNotice(ctx, manager)
	if err != nil {
		t.Fatalf("acknowledge starter notice: %v", err)
	}
	if acknowledged.StarterPolicyReviewRequired || acknowledged.StarterPolicyNoticeAcknowledgedAt == nil {
		t.Fatalf("starter notice did not become one-time acknowledged state: %+v", acknowledged)
	}
	starter, err := repositories.GetCaseTemplateExpanded(ctx, bootstrap.Guild.ID, bootstrap.StarterTemplate.Template.ID)
	if err != nil || starter == nil || starter.Template.ArchivedAt != nil {
		t.Fatalf("acknowledgement changed starter policy availability: starter=%+v err=%v", starter, err)
	}
	read, err := service.Get(ctx, manager)
	if err != nil || read.StarterPolicyReviewRequired {
		t.Fatalf("read did not expose acknowledged setup state: read=%+v err=%v", read, err)
	}
}

// allowStaffChannel isolates settings persistence tests from the live Discord adapter.
type allowStaffChannel struct{}

func (allowStaffChannel) ValidateStaffChannel(context.Context, string, string) error { return nil }
