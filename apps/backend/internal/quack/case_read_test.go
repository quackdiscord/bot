package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestCaseServiceDashboardReads(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))

	first, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatalf("create first case: %v", err)
	}
	second, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-2"})
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}

	list, err := service.List(ctx, modContext, quack.CaseListInput{Limit: "10"})
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if list.Total != 2 || len(list.Cases) != 2 || list.Cases[0].ID != second.ID || list.Cases[1].ID != first.ID {
		t.Fatalf("expected newest-first case list, got %+v", list)
	}
	if list.Cases[0].SelectedLevel == nil {
		t.Fatalf("expected selected level in case list response")
	}
	legacySnapshot := fmt.Sprintf(`{"template":{"id":%q,"slug":"spam","name":"Spam","version":1,"reason_template":"No spam","default_severity":"medium"},"selected_level":{"id":"level-1","name":"Default","position":1,"is_default":true,"trigger_case_count":0,"window_minutes":0,"notify_user":true,"notification_type":"warning","matched_case_count":1},"actions":[{"id":"action-1","position":1,"action_type":"timeout_user","config":{"duration_minutes":60},"notify_user":false,"continue_on_error":false,"max_retries":2,"retry_backoff_ms":1000,"timeout_ms":0,"idempotency_scope":"case"}]}`, template.ID)
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", first.ID).Update("template_snapshot_json", legacySnapshot).Error; err != nil {
		t.Fatalf("install legacy snapshot fixture: %v", err)
	}

	detail, err := service.Get(ctx, modContext, "1")
	if err != nil {
		t.Fatalf("get case detail: %v", err)
	}
	if detail.ID != first.ID || detail.TemplateSnapshot == nil || len(detail.Events) != 1 || len(detail.Actions) != 0 || detail.Notification == nil {
		t.Fatalf("unexpected case detail: %+v", detail)
	}
	if len(detail.TemplateSnapshot.Actions) != 1 || detail.TemplateSnapshot.Actions[0].TimeoutDurationSeconds != 3600 || detail.TemplateSnapshot.Actions[0].MaxRetries != 2 {
		t.Fatalf("expected legacy snapshot settings to remain readable, got %+v", detail.TemplateSnapshot.Actions)
	}

	profile, err := service.UserHistory(ctx, modContext, "target-1", quack.CaseListInput{Limit: "10"})
	if err != nil {
		t.Fatalf("user history: %v", err)
	}
	if profile.Total != 1 || len(profile.Cases) != 1 || profile.Summary.Total != 1 || profile.Summary.ByValidity[string(quack.CaseValidityValid)] != 1 {
		t.Fatalf("unexpected profile response: %+v", profile)
	}
	if profile.Summary.ByTemplate[template.ID] != 1 {
		t.Fatalf("expected profile summary by template, got %+v", profile.Summary.ByTemplate)
	}
}

func TestCaseServiceReadValidationAndPermissions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewCaseService(store, nil, nil, nil)
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("spam"))
	if _, err := service.Create(ctx, modContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"}); err != nil {
		t.Fatalf("create case: %v", err)
	}

	_, err := service.List(ctx, modContext, quack.CaseListInput{Limit: "0"})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected limit validation error, got %v", err)
	}
	_, err = service.List(ctx, modContext, quack.CaseListInput{Validity: "not-a-validity"})
	if !errors.Is(err, quack.ErrCaseValidation) {
		t.Fatalf("expected status validation error, got %v", err)
	}
	_, err = service.Get(ctx, modContext, "missing")
	if !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("expected not found error, got %v", err)
	}

	noReadContext := *modContext
	noReadContext.Permissions = map[quack.PermissionAction]bool{quack.PermissionActionCaseCreate: false}
	_, err = service.List(ctx, &noReadContext, quack.CaseListInput{})
	if !errors.Is(err, quack.ErrCasePermissionDenied) {
		t.Fatalf("expected permission error, got %v", err)
	}
}

func TestCaseEvidenceResponsesUseStableJSONNames(t *testing.T) {
	body, err := json.Marshal(quack.CaseEvidenceResponse{
		ID:                  "evidence-1",
		AuthorDiscordUserID: "member-1",
		MessageURL:          "https://discord.com/channels/1/2/3",
		MessageCreatedAt:    time.Now().UTC(),
		Attachments: []quack.CaseEvidenceAttachmentResponse{{
			Filename: "proof.png", ContentType: "image/png", OriginalURL: "https://cdn.example/original", SizeBytes: 10,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(body)
	for _, key := range []string{`"author_discord_user_id"`, `"message_url"`, `"message_created_at"`, `"content_type"`, `"original_url"`, `"size_bytes"`} {
		if !strings.Contains(encoded, key) {
			t.Fatalf("evidence response omitted stable JSON key %s: %s", key, encoded)
		}
	}
	if strings.Contains(encoded, "AuthorDiscordUserID") || strings.Contains(encoded, "OriginalURL") {
		t.Fatalf("evidence response leaked Go field names: %s", encoded)
	}
}
