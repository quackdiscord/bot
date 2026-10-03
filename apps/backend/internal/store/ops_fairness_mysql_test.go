package store

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestMySQLExecutableCaseSelectionIsGuildFair(t *testing.T) {
	db := openMySQLMigrationDB(t)
	repository := New(db, nil)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate MySQL fairness fixture: %v", err)
	}
	ctx := context.Background()
	firstGuild, err := repository.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "mysql-fair-1", Name: "Fair One", OwnerDiscordUserID: "owner-1"})
	if err != nil {
		t.Fatalf("upsert first MySQL guild: %v", err)
	}
	secondGuild, err := repository.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "mysql-fair-2", Name: "Fair Two", OwnerDiscordUserID: "owner-2"})
	if err != nil {
		t.Fatalf("upsert second MySQL guild: %v", err)
	}
	caseGuild := map[string]string{}
	for range 3 {
		caseGuild[createMySQLExecutableCase(t, repository, firstGuild.ID)] = firstGuild.ID
	}
	caseGuild[createMySQLExecutableCase(t, repository, secondGuild.ID)] = secondGuild.ID

	caseIDs, err := repository.ListExecutableCaseIDs(ctx, 2)
	if err != nil {
		t.Fatalf("list MySQL executable cases: %v", err)
	}
	if len(caseIDs) != 2 || caseGuild[caseIDs[0]] == caseGuild[caseIDs[1]] {
		t.Fatalf("expected one MySQL case per guild before a second busy-guild case, got %+v", caseIDs)
	}
}

func createMySQLExecutableCase(t *testing.T, repository *Store, guildID string) string {
	t.Helper()
	created, err := repository.CreateCase(context.Background(), quack.CreateCaseParams{
		Case: quack.Case{
			GuildID: guildID, TemplateVersion: 1, TemplateSnapshotJSON: "{}",
			TargetDiscordUserID: "target", ModeratorDiscordUserID: "moderator",
			Reason: "reason", Validity: quack.CaseValidityValid, Source: quack.CaseSourceDashboard, MetadataJSON: "{}",
		},
		Event:            quack.CaseEvent{EventType: quack.CaseEventCreated, Body: "created"},
		ActionExecutions: []quack.CaseActionExecution{{Position: 1, ActionType: quack.ActionTimeoutUser, ConfigSnapshotJSON: "{}"}},
	})
	if err != nil {
		t.Fatalf("create MySQL executable case: %v", err)
	}
	return created.Case.ID
}
