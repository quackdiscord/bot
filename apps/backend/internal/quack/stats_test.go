package quack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestStaffStatisticsAreGuildScopedDerivedAndUnranked(t *testing.T) {
	ctx := context.Background()
	repository := newMigratedStore(t)
	moderator := templateGuildContext(t, repository, "stats-guild", "moderator", uint64(discordgo.PermissionModerateMembers))
	other := templateGuildContext(t, repository, "other-stats-guild", "other", uint64(discordgo.PermissionModerateMembers))
	now := time.Now().UTC()
	baselineAudits, err := repository.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: moderator.Guild.ID, CreatedAfter: now.Add(-24 * time.Hour).Format(time.RFC3339Nano), CreatedBefore: now.Add(time.Hour).Format(time.RFC3339Nano), Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	templateID := "01J00000000000000000000001"
	caseID := "01J00000000000000000000002"
	otherCaseID := "01J00000000000000000000003"
	rows := []quack.Case{
		{ULIDModel: quack.ULIDModel{ID: caseID, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: moderator.Guild.ID, CaseNumber: 1, TemplateID: &templateID, TargetDiscordUserID: "member", ModeratorDiscordUserID: "moderator", Reason: "Rule", Validity: quack.CaseValidityValid, Source: quack.CaseSourceDiscord, MetadataJSON: "{}", ContextValuesJSON: "{}", TemplateSnapshotJSON: "{}"},
		{ULIDModel: quack.ULIDModel{ID: otherCaseID, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: other.Guild.ID, CaseNumber: 1, TargetDiscordUserID: "member", ModeratorDiscordUserID: "other", Reason: "Other", Validity: quack.CaseValidityValid, Source: quack.CaseSourceDiscord, MetadataJSON: "{}", ContextValuesJSON: "{}", TemplateSnapshotJSON: "{}"},
	}
	for i := range rows {
		if err := repository.DB().Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	action := quack.CaseActionExecution{ULIDModel: quack.ULIDModel{ID: "01J00000000000000000000004", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, CaseID: caseID, ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded, IdempotencyKey: "stats-action", ConfigSnapshotJSON: "{}"}
	appealCaseID := caseID
	appeal := quack.Appeal{ULIDModel: quack.ULIDModel{ID: "01J00000000000000000000005", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}, GuildID: moderator.Guild.ID, CaseID: &appealCaseID, TargetDiscordUserID: "member", Status: quack.AppealStatusAccepted, MetadataJSON: "{}"}
	if err := repository.DB().Create(&action).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Create(&appeal).Error; err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateAuditLogEntry(ctx, &quack.AuditLogEntry{GuildID: moderator.Guild.ID, Source: quack.AuditSourceDiscord, Action: string(quack.AuditActionCaseCreate), ResourceType: "case", ResourceID: caseID, Result: quack.AuditResultSuccess, MetadataJSON: "{}"}); err != nil {
		t.Fatal(err)
	}

	service := quack.NewStaffStatisticsService(repository)
	result, err := service.Get(ctx, moderator, quack.StatisticsInput{From: now.Add(-24 * time.Hour).Format(time.RFC3339), To: now.Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseTotal != 1 || result.ActionTotal != 1 || result.AppealTotal != 1 || result.AuditTotal != baselineAudits.Total+1 {
		t.Fatalf("statistics crossed guild boundaries or persisted a second truth: %+v", result)
	}
	if len(result.CasesByTemplate) != 1 || result.CasesByTemplate[0].Key != templateID || len(result.ActionsByType) != 1 || result.ActionsByType[0].Key != string(quack.ActionTimeoutUser) || len(result.AppealsByStatus) != 1 || result.AppealsByStatus[0].Key != string(quack.AppealStatusAccepted) {
		t.Fatalf("missing required breakdowns: %+v", result)
	}
	ordinary := templateGuildContext(t, repository, "stats-guild", "ordinary", uint64(discordgo.PermissionSendMessages))
	if _, err := service.Get(ctx, ordinary, quack.StatisticsInput{}); !errors.Is(err, quack.ErrStatisticsPermissionDenied) {
		t.Fatalf("expected statistics permission denial, got %v", err)
	}
}
