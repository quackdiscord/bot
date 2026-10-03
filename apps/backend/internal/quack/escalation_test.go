package quack_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestCaseServiceSelectsEscalationLevelFromSameTemplateHistory(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	for i := 0; i < 2; i++ {
		created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
		if err != nil {
			t.Fatalf("create prior case %d: %v", i+1, err)
		}
		if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault {
			t.Fatalf("expected prior case to use default level, got %+v", created.SelectedLevel)
		}
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.IsDefault || created.SelectedLevel.TriggerCaseCount != 3 || created.SelectedLevel.MatchedCaseCount != 3 {
		t.Fatalf("expected repeat spam level, got %+v", created.SelectedLevel)
	}
	if len(created.Actions) != 1 || created.Actions[0].ActionType != quack.ActionTimeoutUser {
		t.Fatalf("expected selected escalation action only, got %+v", created.Actions)
	}

	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	var snapshot struct {
		SelectedLevel struct {
			TriggerCaseCount int   `json:"trigger_case_count"`
			MatchedCaseCount int64 `json:"matched_case_count"`
		} `json:"selected_level"`
		Actions []struct {
			ActionType quack.ActionType `json:"action_type"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(cases[2].TemplateSnapshotJSON), &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.SelectedLevel.TriggerCaseCount != 3 || snapshot.SelectedLevel.MatchedCaseCount != 3 || len(snapshot.Actions) != 1 || snapshot.Actions[0].ActionType != quack.ActionTimeoutUser {
		t.Fatalf("unexpected escalation snapshot: %+v", snapshot)
	}
	assertSimplifiedCaseSnapshot(t, cases[2].TemplateSnapshotJSON)
}

func assertSimplifiedCaseSnapshot(t *testing.T, body string) {
	t.Helper()
	var snapshot map[string]any
	if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
		t.Fatalf("decode simplified case snapshot: %v", err)
	}
	template := snapshot["template"].(map[string]any)
	if _, exists := template["default_severity"]; exists {
		t.Fatalf("case snapshot leaked template severity: %s", body)
	}
	level := snapshot["selected_level"].(map[string]any)
	for _, retired := range []string{"window_minutes", "notification_type", "enabled"} {
		if _, exists := level[retired]; exists {
			t.Fatalf("case snapshot leaked level field %s: %s", retired, body)
		}
	}
	for _, rawAction := range snapshot["actions"].([]any) {
		action := rawAction.(map[string]any)
		for _, retired := range []string{"position", "config", "notify_user", "notification_type", "continue_on_error", "retry_backoff_ms", "timeout_ms", "idempotency_scope", "enabled"} {
			if _, exists := action[retired]; exists {
				t.Fatalf("case snapshot leaked action field %s: %s", retired, body)
			}
		}
	}
}

func TestCaseServiceHighestMatchingLevelWins(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	input := validTemplateInput("spam")
	input.Levels = append(input.Levels, quack.TemplateLevelInput{
		Name:             "Early repeat",
		Position:         3,
		TriggerCaseCount: 2,
	})
	template := createAppTemplate(t, ctx, store, adminContext, input)

	for i := 0; i < 2; i++ {
		if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
			t.Fatalf("create prior case %d: %v", i+1, err)
		}
	}
	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.TriggerCaseCount != 3 {
		t.Fatalf("expected highest trigger threshold to win, got %+v", created.SelectedLevel)
	}
}

func TestCaseServiceEscalationUsesAllTimeMatchingHistory(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	input := validTemplateInput("spam")
	input.Levels[1].TriggerCaseCount = 2
	template := createAppTemplate(t, ctx, store, adminContext, input)

	old, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create old case: %v", err)
	}
	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", old.ID).Update("created_at", oldTime).Error; err != nil {
		t.Fatalf("age old case: %v", err)
	}

	otherTemplate := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("other-template"))
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: otherTemplate.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create other template case: %v", err)
	}
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-2"}); err != nil {
		t.Fatalf("create other target case: %v", err)
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 2 {
		t.Fatalf("expected old matching history to count without a time window, got %+v", created.SelectedLevel)
	}
}

func TestCaseServiceVoidedCasesDoNotCount(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)

	input := validTemplateInput("spam")
	input.Levels[1].TriggerCaseCount = 2
	template := createAppTemplate(t, ctx, store, adminContext, input)

	prior, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create prior case: %v", err)
	}
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", prior.ID).Update("validity", quack.CaseValidityVoided).Error; err != nil {
		t.Fatalf("void prior case: %v", err)
	}

	created, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.SelectedLevel == nil || !created.SelectedLevel.IsDefault || created.SelectedLevel.MatchedCaseCount != 1 {
		t.Fatalf("expected voided prior case to be ignored, got %+v", created.SelectedLevel)
	}
}

func TestEscalationExcludesImportedV4HistoryAcrossTemplateVersions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewTemplateService(store)
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("versioned-policy"))
	templateID := template.ID
	if _, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: quack.Case{GuildID: moderator.Guild.ID, TemplateID: &templateID, TemplateVersion: 1, TemplateSnapshotJSON: "{}", TargetDiscordUserID: "target-1", ModeratorDiscordUserID: "importer", Reason: "legacy", Validity: quack.CaseValidityValid, Source: quack.CaseSourceV4Import, MetadataJSON: "{}", ContextValuesJSON: "[]"}, Event: quack.CaseEvent{EventType: quack.CaseEventCreated, Body: "imported", MetadataJSON: "{}"}}); err != nil {
		t.Fatal(err)
	}
	update := validTemplateInput("versioned-policy")
	update.Description = "version two"
	updated, err := service.Update(ctx, admin, template.ID, update)
	if err != nil || updated.ID != template.ID || updated.Version != 2 {
		t.Fatalf("version update: %+v err=%v", updated, err)
	}
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	if created.SelectedLevel == nil || created.SelectedLevel.MatchedCaseCount != 1 || !created.SelectedLevel.IsDefault {
		t.Fatalf("v4 history affected escalation: %+v", created.SelectedLevel)
	}
}
