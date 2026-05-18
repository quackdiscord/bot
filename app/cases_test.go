package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestCaseServiceCreateFromTemplate(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := app.NewCaseService(store)

	created, err := service.Create(ctx, modContext, app.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
		Metadata:            json.RawMessage(`{"message_id":"123"}`),
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.ID == "" || created.CaseNumber != 1 {
		t.Fatalf("unexpected case response: %+v", created)
	}
	if created.Reason != "No spam" || created.Status != structs.CaseStatusOpen || created.Source != structs.CaseSourceAPI {
		t.Fatalf("unexpected case fields: %+v", created)
	}
	if len(created.Actions) != 2 {
		t.Fatalf("expected two enabled actions, got %+v", created.Actions)
	}
	if created.Actions[0].Position != 1 || created.Actions[0].Status != structs.ActionExecutionPending {
		t.Fatalf("unexpected first action: %+v", created.Actions[0])
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		Template struct {
			ID string `json:"id"`
		} `json:"template"`
		Actions []struct {
			ActionType structs.ActionType `json:"action_type"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(cases[0].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Template.ID != template.ID || len(snapshot.Actions) != 2 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestCaseServiceRejectsUnavailableTemplates(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	templateService := app.NewTemplateService(store)
	caseService := app.NewCaseService(store)

	disabled := false
	disabledInput := validTemplateInput("disabled")
	disabledInput.Enabled = &disabled
	disabledTemplate := createAppTemplate(t, ctx, store, adminContext, disabledInput)

	archivedTemplate := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("archived"))
	if _, err := templateService.Archive(ctx, adminContext, archivedTemplate.ID); err != nil {
		t.Fatalf("archive template: %v", err)
	}

	tests := []struct {
		name       string
		templateID string
	}{
		{name: "missing", templateID: "missing-template"},
		{name: "disabled", templateID: disabledTemplate.ID},
		{name: "archived", templateID: archivedTemplate.ID},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := caseService.Create(ctx, modContext, app.CaseInput{
				TemplateID:          tt.templateID,
				TargetDiscordUserID: "target-1",
			})
			if !errors.Is(err, app.ErrCaseTemplateNotAvailable) {
				t.Fatalf("expected unavailable template error, got %v", err)
			}
		})
	}
}

func TestCaseServiceValidationFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	service := app.NewCaseService(store)

	tests := []struct {
		name  string
		input app.CaseInput
	}{
		{name: "missing template", input: app.CaseInput{TargetDiscordUserID: "target-1"}},
		{name: "missing target", input: app.CaseInput{TemplateID: template.ID}},
		{name: "invalid source", input: app.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Source: "invalid"}},
		{name: "invalid metadata", input: app.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1", Metadata: json.RawMessage(`[]`)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.Create(ctx, modContext, tt.input)
			if !errors.Is(err, app.ErrCaseValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestCaseServiceRejectsEmptyFinalReason(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	created, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: structs.CaseTemplate{
			GuildID:                guildContext.Guild.ID,
			Slug:                   "empty-reason",
			Name:                   "Empty Reason",
			ReasonTemplate:         " ",
			DefaultSeverity:        structs.CaseSeverityMedium,
			DefaultWeight:          1,
			Enabled:                true,
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Actions: []structs.CaseTemplateAction{{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = app.NewCaseService(store).Create(ctx, guildContext, app.CaseInput{
		TemplateID:          created.Template.ID,
		TargetDiscordUserID: "target-1",
	})
	if !errors.Is(err, app.ErrCaseValidation) {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestCaseServicePermissionFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := app.NewCaseService(store)

	noCreateContext := *modContext
	noCreateContext.Permissions = map[structs.PermissionAction]bool{structs.PermissionActionCaseCreate: false}
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	_, err := service.Create(ctx, &noCreateContext, app.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if !errors.Is(err, app.ErrCasePermissionDenied) {
		t.Fatalf("expected case.create permission error, got %v", err)
	}

	templateRequiredInput := validTemplateInput("manage-template")
	templateRequiredInput.RequiredPermissionBits = uint64(discordgo.PermissionManageGuild)
	templateRequired := createAppTemplate(t, ctx, store, adminContext, templateRequiredInput)
	_, err = service.Create(ctx, modContext, app.CaseInput{TemplateID: templateRequired.ID, TargetDiscordUserID: "target-1"})
	if !errors.Is(err, app.ErrCasePermissionDenied) {
		t.Fatalf("expected template permission error, got %v", err)
	}

	actionRequiredInput := validTemplateInput("manage-action")
	actionRequiredInput.Actions[0].RequiredPermissionBits = uint64(discordgo.PermissionManageGuild)
	actionRequired := createAppTemplate(t, ctx, store, adminContext, actionRequiredInput)
	_, err = service.Create(ctx, modContext, app.CaseInput{TemplateID: actionRequired.ID, TargetDiscordUserID: "target-1"})
	if !errors.Is(err, app.ErrCasePermissionDenied) {
		t.Fatalf("expected action permission error, got %v", err)
	}
}

func TestCaseServiceSnapshotIncludesMatchedEscalation(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := app.NewCaseService(store)

	input := validTemplateInput("spam")
	input.EscalationRules[0].TriggerCaseCount = 1
	template := createAppTemplate(t, ctx, store, adminContext, input)
	if _, err := service.Create(ctx, modContext, app.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create first case: %v", err)
	}
	if _, err := service.Create(ctx, modContext, app.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create second case: %v", err)
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		Escalation struct {
			MatchedRules []struct {
				ID        string `json:"id"`
				CaseCount int64  `json:"case_count"`
			} `json:"matched_rules"`
		} `json:"escalation"`
	}
	if err := json.Unmarshal([]byte(cases[1].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snapshot.Escalation.MatchedRules) != 1 || snapshot.Escalation.MatchedRules[0].CaseCount != 1 {
		t.Fatalf("expected matched escalation in second snapshot, got %+v", snapshot.Escalation.MatchedRules)
	}
}

func createAppTemplate(t *testing.T, ctx context.Context, store *storage.Store, guildContext *app.GuildStaffContext, input app.TemplateInput) *app.TemplateResponse {
	t.Helper()

	created, err := app.NewTemplateService(store).Create(ctx, guildContext, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}
