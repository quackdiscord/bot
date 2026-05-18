package storage_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestCaseTemplateStorageCreateListGetExpanded(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions: []structs.CaseTemplateAction{
			{Position: 2, ActionType: structs.ActionSendDM, ConfigJSON: `{"message":"stop"}`, IdempotencyScope: "case", Enabled: true},
			{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true},
		},
		EscalationRules: []structs.CaseTemplateEscalationRule{
			{Name: "Second", Scope: structs.EscalationScopeUser, Priority: 20, RuleConfigJSON: `{}`, Enabled: true, StopAfterMatch: true},
			{Name: "First", Scope: structs.EscalationScopeUser, Priority: 10, RuleConfigJSON: `{}`, Enabled: true, StopAfterMatch: true},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	if created.Template.ID == "" {
		t.Fatalf("expected template id")
	}
	if len(created.Actions) != 2 || created.Actions[0].Position != 1 || created.Actions[1].Position != 2 {
		t.Fatalf("expected actions ordered by position, got %+v", created.Actions)
	}
	if len(created.EscalationRules) != 2 || created.EscalationRules[0].Priority != 10 {
		t.Fatalf("expected rules ordered by priority, got %+v", created.EscalationRules)
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(list) != 1 || list[0].Template.ID != created.Template.ID {
		t.Fatalf("expected created template in list, got %+v", list)
	}
}

func TestCaseTemplateStorageUpdateReplacesChildrenAndIncrementsVersion(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions:  []structs.CaseTemplateAction{{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	update := templateModel(guildID, "spam-updated")
	update.Name = "Spam Updated"
	update.UpdatedByDiscordUserID = "moderator-2"
	updated, err := store.UpdateCaseTemplate(ctx, storage.UpdateCaseTemplateParams{
		GuildID:    guildID,
		TemplateID: created.Template.ID,
		Template:   update,
		Actions: []structs.CaseTemplateAction{
			{Position: 1, ActionType: structs.ActionWriteModLog, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true},
			{Position: 2, ActionType: structs.ActionSendDM, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if updated.Template.Version != created.Template.Version+1 {
		t.Fatalf("expected version increment, got %d then %d", created.Template.Version, updated.Template.Version)
	}
	if len(updated.Actions) != 2 || updated.Actions[0].ActionType != structs.ActionWriteModLog {
		t.Fatalf("expected replaced actions, got %+v", updated.Actions)
	}
}

func TestCaseTemplateStorageArchiveHidesFromListButDetailStillWorks(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions:  []structs.CaseTemplateAction{{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	archived, err := store.ArchiveCaseTemplate(ctx, guildID, created.Template.ID, nil)
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	if archived.Template.ArchivedAt == nil || archived.Template.Enabled {
		t.Fatalf("expected archived disabled template")
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected archived template hidden from list")
	}

	detail, err := store.GetCaseTemplateExpanded(ctx, guildID, created.Template.ID)
	if err != nil {
		t.Fatalf("get archived detail: %v", err)
	}
	if detail == nil || detail.Template.ID != created.Template.ID {
		t.Fatalf("expected archived detail fetch to work")
	}
}

func TestCaseTemplateStorageSlugUniquePerGuild(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	_, err := store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions:  []structs.CaseTemplateAction{{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true}},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = store.CreateCaseTemplate(ctx, storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions:  []structs.CaseTemplateAction{{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true}},
	})
	if err == nil {
		t.Fatalf("expected duplicate slug error")
	}
}

func templateTestStore(t *testing.T) (*storage.Store, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	guild, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	return store, guild.ID
}

func templateModel(guildID, slug string) structs.CaseTemplate {
	return structs.CaseTemplate{
		GuildID:                guildID,
		Slug:                   slug,
		Name:                   "Spam",
		Description:            "Spam template",
		ReasonTemplate:         "No spam",
		DefaultSeverity:        structs.CaseSeverityMedium,
		DefaultWeight:          1,
		Enabled:                true,
		CreatedByDiscordUserID: "moderator-1",
		UpdatedByDiscordUserID: "moderator-1",
	}
}
