package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestCaseTemplateStorageCreateListGetExpanded(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels: []quack.ExpandedCaseTemplateLevel{
			{
				Level: quack.CaseTemplateLevel{Position: 2, Name: "Second", TriggerCaseCount: 3},
				Actions: []quack.CaseTemplateLevelAction{
					{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
			{
				Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true, NotifyUser: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	if created.Template.ID == "" {
		t.Fatalf("expected template id")
	}
	if len(created.Levels) != 2 || created.Levels[0].Level.Position != 1 || created.Levels[1].Level.Position != 2 {
		t.Fatalf("expected levels ordered by position, got %+v", created.Levels)
	}
	if len(created.Levels[0].Actions) != 0 || len(created.Levels[1].Actions) != 1 {
		t.Fatalf("expected zero or one action per level, got %+v", created.Levels)
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

	created, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	update := templateModel(guildID, "spam-updated")
	update.Name = "Spam Updated"
	update.UpdatedByDiscordUserID = "moderator-2"
	updated, err := store.UpdateCaseTemplate(ctx, quack.UpdateCaseTemplateParams{
		GuildID:    guildID,
		TemplateID: created.Template.ID,
		Template:   update,
		Levels: []quack.ExpandedCaseTemplateLevel{
			{
				Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
				Actions: []quack.CaseTemplateLevelAction{
					{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if updated.Template.Version != created.Template.Version+1 {
		t.Fatalf("expected version increment, got %d then %d", created.Template.Version, updated.Template.Version)
	}
	if len(updated.Levels) != 1 || len(updated.Levels[0].Actions) != 1 || updated.Levels[0].Actions[0].ActionType != quack.ActionTimeoutUser {
		t.Fatalf("expected replaced levels and actions, got %+v", updated.Levels)
	}
}

func TestCaseTemplateStorageArchiveHidesFromListButDetailStillWorks(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	archived, err := store.ArchiveCaseTemplate(ctx, guildID, created.Template.ID, nil)
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	if archived.Template.ArchivedAt == nil {
		t.Fatalf("expected archived template")
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(list) != 1 || list[0].Template.ArchivedAt == nil {
		t.Fatalf("expected archived template retained in authorized list")
	}

	detail, err := store.GetCaseTemplateExpanded(ctx, guildID, created.Template.ID)
	if err != nil {
		t.Fatalf("get archived detail: %v", err)
	}
	if detail == nil || detail.Template.ID != created.Template.ID {
		t.Fatalf("expected archived detail fetch to work")
	}
}

func TestCaseTemplateStorageListOmitsQuarantinedTemplates(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "legacy-policy"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	now := time.Now().UTC()
	if err := store.DB().Exec(
		"INSERT INTO quack_v5_0002_template_compatibility (template_id, previous_archived_at, previous_deleted_at, reason, recorded_at) VALUES (?, ?, ?, ?, ?)",
		created.Template.ID, nil, nil, "level uses an escalation window", now,
	).Error; err != nil {
		t.Fatalf("record template compatibility state: %v", err)
	}

	list, err := store.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatalf("list templates with quarantined policy: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("quarantined template crossed live list boundary: %+v", list)
	}

	detail, err := store.GetCaseTemplateExpanded(ctx, guildID, created.Template.ID)
	if detail != nil || !errors.Is(err, quack.ErrTemplateCompatibilityReviewRequired) {
		t.Fatalf("expected detail compatibility conflict, detail=%+v err=%v", detail, err)
	}
}

func TestCaseTemplateStorageSlugUniquePerGuild(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	_, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels:   templateLevels(),
	})
	if err == nil {
		t.Fatalf("expected duplicate slug error")
	}
}

func templateLevels() []quack.ExpandedCaseTemplateLevel {
	return []quack.ExpandedCaseTemplateLevel{
		{
			Level: quack.CaseTemplateLevel{
				Position:  1,
				Name:      "Default",
				IsDefault: true,
			},
			Actions: []quack.CaseTemplateLevelAction{
				{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
			},
		},
	}
}

func templateTestStore(t *testing.T) (*storage.Store, string) {
	t.Helper()

	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	return store, guild.ID
}

func templateModel(guildID, slug string) quack.CaseTemplate {
	return quack.CaseTemplate{
		GuildID:                guildID,
		Slug:                   slug,
		Name:                   "Spam",
		Description:            "Spam template",
		ReasonTemplate:         "No spam",
		CreatedByDiscordUserID: "moderator-1",
		UpdatedByDiscordUserID: "moderator-1",
	}
}
