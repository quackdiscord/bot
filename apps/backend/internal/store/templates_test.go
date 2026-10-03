package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestCreateTemplateExpandsInOrder(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created, err := s.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: newTemplate(guildID, "spam"),
		ContextFields: []quack.CaseTemplateContextField{
			{Key: "second", Label: "Second", FieldType: quack.ContextFieldShortText, Position: 2},
			{Key: "first", Label: "First", FieldType: quack.ContextFieldBoolean, Position: 1, Required: true},
		},
		Levels: []quack.ExpandedCaseTemplateLevel{
			{
				Level:   quack.CaseTemplateLevel{Position: 2, Name: "Second", TriggerCaseCount: 3},
				Actions: []quack.CaseTemplateLevelAction{{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`}},
			},
			{Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true, NotifyUser: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Template.ID == "" || created.Template.Version != 1 {
		t.Fatalf("template = %+v", created.Template)
	}
	if len(created.ContextFields) != 2 || created.ContextFields[0].Key != "first" || !created.ContextFields[0].Required {
		t.Fatalf("context fields = %+v", created.ContextFields)
	}
	levels := created.Levels
	if len(levels) != 2 || levels[0].Level.Position != 1 || !levels[0].Level.NotifyUser || len(levels[0].Actions) != 0 ||
		len(levels[1].Actions) != 1 || levels[1].Actions[0].LevelID != levels[1].Level.ID {
		t.Fatalf("levels = %+v", levels)
	}
	if got, err := s.GetCaseTemplateBySlug(ctx, guildID, "spam"); err != nil || got.ID != created.Template.ID {
		t.Fatalf("GetCaseTemplateBySlug = %+v, %v", got, err)
	}
	if err := notFound(s.GetCaseTemplateBySlug(ctx, guildID, "missing")); err != nil {
		t.Error(err)
	}
	other := addGuild(t, s, "guild-2")
	if err := notFound(s.GetCaseTemplateExpanded(ctx, other, created.Template.ID)); err != nil {
		t.Errorf("template read across guilds: %v", err)
	}
}

func TestListTemplatesLoadsChildrenForEveryTemplate(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	for _, slug := range []string{"b-second", "a-first", "c-third"} {
		createTemplate(t, s, guildID, slug)
	}
	list, err := s.ListCaseTemplates(ctx, guildID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Template.Slug != "a-first" || list[2].Template.Slug != "c-third" {
		t.Fatalf("templates not ordered by slug: %+v", list)
	}
	for _, template := range list {
		if len(template.Levels) != 2 || len(template.Levels[1].Actions) != 1 || template.ContextFields == nil {
			t.Fatalf("template %s expanded wrong: %+v", template.Template.Slug, template)
		}
	}
	empty, err := s.ListCaseTemplates(ctx, "no-such-guild")
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty guild = %#v, %v; want an empty slice", empty, err)
	}
}

func TestUpdateTemplateReplacesChildrenAndBumpsVersion(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createTemplate(t, s, guildID, "spam")
	update := newTemplate(guildID, "spam-updated")
	update.Name = "Spam Updated"
	update.UpdatedByDiscordUserID = "moderator-2"
	updated, err := s.UpdateCaseTemplate(ctx, quack.UpdateCaseTemplateParams{
		GuildID: guildID, TemplateID: created.Template.ID, Template: update,
		Levels: []quack.ExpandedCaseTemplateLevel{{
			Level:   quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
			Actions: []quack.CaseTemplateLevelAction{{ActionType: quack.ActionKickUser, ConfigJSON: `{}`}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Template.Version != 2 || updated.Template.Slug != "spam-updated" || updated.Template.UpdatedByDiscordUserID != "moderator-2" {
		t.Fatalf("template = %+v", updated.Template)
	}
	if len(updated.Levels) != 1 || len(updated.Levels[0].Actions) != 1 || updated.Levels[0].Actions[0].ActionType != quack.ActionKickUser {
		t.Fatalf("levels = %+v", updated.Levels)
	}
	var orphans int64
	if err := s.DB().Table("case_template_level_actions").Where("level_id NOT IN (SELECT id FROM case_template_levels)").Count(&orphans).Error; err != nil || orphans != 0 {
		t.Fatalf("update left %d orphaned actions (%v)", orphans, err)
	}
	if err := notFound(s.UpdateCaseTemplate(ctx, quack.UpdateCaseTemplateParams{GuildID: guildID, TemplateID: "missing", Template: update})); err != nil {
		t.Errorf("update of missing template: %v", err)
	}
}

func TestArchiveAndRestoreTemplate(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createTemplate(t, s, guildID, "spam")
	audit := &quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "case_template.archive",
		ResourceType: "case_template", Result: quack.AuditResultSuccess}
	archived, err := s.ArchiveCaseTemplate(ctx, guildID, created.Template.ID, audit)
	if err != nil || archived.Template.ArchivedAt == nil || archived.Template.Version != 1 {
		t.Fatalf("archive = %+v, %v", archived, err)
	}
	list, err := s.ListCaseTemplates(ctx, guildID)
	if err != nil || len(list) != 1 || list[0].Template.ArchivedAt == nil {
		t.Fatalf("archived template missing from list: %+v, %v", list, err)
	}
	restored, err := s.RestoreCaseTemplate(ctx, guildID, created.Template.ID, nil)
	if err != nil || restored.Template.ArchivedAt != nil || restored.Template.Version != 1 {
		t.Fatalf("restore = %+v, %v", restored, err)
	}
	if err := notFound(s.ArchiveCaseTemplate(ctx, "other-guild", created.Template.ID, nil)); err != nil {
		t.Errorf("archive across guilds: %v", err)
	}
	audits, err := s.ListAuditLogEntries(ctx, guildID)
	if err != nil || len(audits) != 1 || audits[0].ResourceID != created.Template.ID {
		t.Fatalf("audits = %+v, %v", audits, err)
	}
}
