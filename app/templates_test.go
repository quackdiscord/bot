package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestTemplateServiceCreateNormalizesActionsAndAudits(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := app.NewTemplateService(store)

	created, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected template id")
	}
	if len(created.Actions) != 2 || created.Actions[0].Position != 1 || created.Actions[1].Position != 2 {
		t.Fatalf("expected normalized action positions, got %+v", created.Actions)
	}

	audits, err := store.ListAuditLogEntries(ctx, guildContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 1 || audits[0].Action != "case_template.create" || audits[0].Result != structs.AuditResultSuccess {
		t.Fatalf("expected successful create audit, got %+v", audits)
	}
}

func TestTemplateServiceValidationFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := app.NewTemplateService(store)

	tests := []struct {
		name string
		edit func(*app.TemplateInput)
	}{
		{name: "invalid slug", edit: func(input *app.TemplateInput) { input.Slug = "Invalid Slug" }},
		{name: "empty reason", edit: func(input *app.TemplateInput) { input.ReasonTemplate = "" }},
		{name: "invalid severity", edit: func(input *app.TemplateInput) { input.DefaultSeverity = "urgent" }},
		{name: "invalid action", edit: func(input *app.TemplateInput) { input.Actions[0].ActionType = "explode_user" }},
		{name: "invalid config", edit: func(input *app.TemplateInput) { input.Actions[0].Config = json.RawMessage(`[]`) }},
		{name: "negative retry", edit: func(input *app.TemplateInput) { input.Actions[0].MaxRetries = -1 }},
		{name: "negative backoff", edit: func(input *app.TemplateInput) { input.Actions[0].RetryBackoffMS = -1 }},
		{name: "negative timeout", edit: func(input *app.TemplateInput) { input.Actions[0].TimeoutMS = -1 }},
		{name: "no enabled actions", edit: func(input *app.TemplateInput) {
			disabled := false
			input.Actions[0].Enabled = &disabled
			input.Actions[1].Enabled = &disabled
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validTemplateInput("spam-" + strings.ReplaceAll(tt.name, " ", "-"))
			tt.edit(&input)
			_, err := service.Create(ctx, guildContext, input)
			if !errors.Is(err, app.ErrTemplateValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestTemplateServiceUpdateAndArchiveAudit(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := app.NewTemplateService(store)

	created, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	update := validTemplateInput("spam-updated")
	update.Name = "Spam Updated"
	update.Actions = update.Actions[:1]
	updated, err := service.Update(ctx, guildContext, created.ID, update)
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Fatalf("expected version increment, got %d then %d", created.Version, updated.Version)
	}
	if len(updated.Actions) != 1 {
		t.Fatalf("expected update to replace actions")
	}

	archived, err := service.Archive(ctx, guildContext, created.ID)
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	if archived.Enabled || archived.ArchivedAt == nil {
		t.Fatalf("expected archived disabled template")
	}

	audits, err := store.ListAuditLogEntries(ctx, guildContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 3 {
		t.Fatalf("expected create/update/archive audits, got %+v", audits)
	}
}

func TestTemplateServiceGuildBoundary(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildOne := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	guildTwo := templateGuildContext(t, store, "guild-2", "user-1", uint64(discordgo.PermissionManageGuild))
	service := app.NewTemplateService(store)

	created, err := service.Create(ctx, guildOne, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = service.Update(ctx, guildTwo, created.ID, validTemplateInput("spam"))
	if !errors.Is(err, app.ErrTemplateNotFound) {
		t.Fatalf("expected not found across guild boundary, got %v", err)
	}
}

func TestTemplateServiceSlugUniqueness(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := app.NewTemplateService(store)

	if _, err := service.Create(ctx, guildContext, validTemplateInput("spam")); err != nil {
		t.Fatalf("create template: %v", err)
	}
	_, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if !errors.Is(err, app.ErrTemplateValidation) {
		t.Fatalf("expected duplicate slug validation, got %v", err)
	}
}

func templateGuildContext(t *testing.T, store *storage.Store, discordGuildID, userID string, permissionBits uint64) *app.GuildStaffContext {
	t.Helper()

	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{ID: discordGuildID, Owner: permissionBits == 0, Permissions: permissionBits}},
		botGuild:   &app.DiscordBotGuild{ID: discordGuildID, Name: "Guild", OwnerID: "owner-1"},
	})
	guildContext, err := service.ResolveStaffContext(context.Background(), testSession(userID), discordGuildID)
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}
	return guildContext
}

func validTemplateInput(slug string) app.TemplateInput {
	return app.TemplateInput{
		Slug:            slug,
		Name:            "Spam",
		Description:     "Spam template",
		ReasonTemplate:  "No spam",
		DefaultSeverity: structs.CaseSeverityMedium,
		DefaultWeight:   1,
		Actions: []app.TemplateActionInput{
			{
				ActionType:       structs.ActionSendDM,
				Config:           json.RawMessage(`{"message":"Please stop"}`),
				MaxRetries:       1,
				RetryBackoffMS:   1000,
				TimeoutMS:        5000,
				IdempotencyScope: "case",
			},
			{
				ActionType:       structs.ActionRecordWarning,
				Config:           json.RawMessage(`{}`),
				IdempotencyScope: "case",
			},
		},
		EscalationRules: []app.EscalationRuleInput{
			{
				Name:             "Repeat spam",
				Scope:            structs.EscalationScopeUser,
				Priority:         10,
				TriggerCaseCount: 3,
				WindowMinutes:    1440,
				RuleConfig:       json.RawMessage(`{"note":"repeat"}`),
			},
		},
	}
}
