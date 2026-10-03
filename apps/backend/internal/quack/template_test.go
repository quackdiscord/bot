package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestTemplateContextLifecycleAndPolicyPortability(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	input := validTemplateInput("context-policy")
	input.ContextFields = []quack.TemplateContextFieldInput{{Key: "summary", Label: "Summary", FieldType: quack.ContextFieldShortText, Position: 1, Required: true}, {Key: "details", Label: "Details", FieldType: quack.ContextFieldLongText, Position: 2}, {Key: "confirmed", Label: "Confirmed", FieldType: quack.ContextFieldBoolean, Position: 3}, {Key: "count", Label: "Count", FieldType: quack.ContextFieldNumber, Position: 4}, {Key: "message", Label: "Message", FieldType: quack.ContextFieldMessageLink, Position: 5}}
	service := quack.NewTemplateService(store)
	created, err := service.Create(ctx, admin, input)
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if len(created.ContextFields) != 5 || created.Version != 1 {
		t.Fatalf("unexpected context template: %+v", created)
	}
	policy, err := service.Export(ctx, admin, created.ID)
	if err != nil {
		t.Fatalf("export policy: %v", err)
	}
	body, _ := json.Marshal(policy)
	for _, forbidden := range []string{admin.Guild.ID, "guild-1", "audit", "channel_id", "secret"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("policy export leaked %q: %s", forbidden, body)
		}
	}
	if _, err := service.Archive(ctx, admin, created.ID); err != nil {
		t.Fatal(err)
	}
	active, err := service.ListActive(ctx, admin)
	if err != nil || len(active) != 0 {
		t.Fatalf("archived template remained active: %+v err=%v", active, err)
	}
	all, err := service.List(ctx, admin)
	if err != nil || len(all) != 1 || all[0].ArchivedAt == nil {
		t.Fatalf("archived template not readable: %+v err=%v", all, err)
	}
	restored, err := service.Restore(ctx, admin, created.ID)
	if err != nil || restored.ArchivedAt != nil {
		t.Fatalf("restore failed: %+v err=%v", restored, err)
	}
	policy.Slug = "imported-policy"
	imported, err := service.Import(ctx, admin, quack.TemplateImportInput{Confirm: true, Policy: *policy})
	if err != nil {
		t.Fatalf("import policy: %v", err)
	}
	if imported.ID == created.ID || imported.GuildID != admin.Guild.ID || imported.ArchivedAt != nil {
		t.Fatalf("import did not create active guild-owned identity: %+v", imported)
	}
}

func TestTemplateServiceCreateNormalizesActionsAndAudits(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	created, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("expected template id")
	}
	if len(created.Levels) != 2 || !created.Levels[0].IsDefault {
		t.Fatalf("expected default and escalation levels, got %+v", created.Levels)
	}
	if len(created.Levels[0].Actions) != 0 {
		t.Fatalf("expected actionless default warning level, got %+v", created.Levels[0].Actions)
	}
	if !created.Levels[0].NotifyUser {
		t.Fatalf("expected default level to notify, got %+v", created.Levels[0])
	}
	if action := created.Levels[1].Actions[0]; action.TimeoutDurationSeconds != 3600 || action.DeleteMessageSeconds != 0 {
		t.Fatalf("expected typed timeout settings, got %+v", action)
	}
	assertSimplifiedTemplateJSON(t, created)

	audits, err := store.ListAuditLogEntries(ctx, guildContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 1 || audits[0].Action != "case_template.create" || audits[0].Result != quack.AuditResultSuccess {
		t.Fatalf("expected successful create audit, got %+v", audits)
	}
}

func assertSimplifiedTemplateJSON(t *testing.T, template *quack.TemplateResponse) {
	t.Helper()
	body, err := json.Marshal(template)
	if err != nil {
		t.Fatalf("marshal template response: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode template response: %v", err)
	}
	if _, exists := decoded["enabled"]; exists {
		t.Fatalf("template response leaked enabled: %s", body)
	}
	levels := decoded["levels"].([]any)
	for _, rawLevel := range levels {
		level := rawLevel.(map[string]any)
		for _, retired := range []string{"enabled", "window_minutes", "notification_type"} {
			if _, exists := level[retired]; exists {
				t.Fatalf("level response leaked %s: %s", retired, body)
			}
		}
		for _, rawAction := range level["actions"].([]any) {
			action := rawAction.(map[string]any)
			for _, retired := range []string{"position", "config", "notify_user", "notification_type", "continue_on_error", "retry_backoff_ms", "timeout_ms", "idempotency_scope", "enabled"} {
				if _, exists := action[retired]; exists {
					t.Fatalf("action response leaked %s: %s", retired, body)
				}
			}
		}
	}
}

func TestTemplateServiceValidationFailures(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	tests := []struct {
		name string
		edit func(*quack.TemplateInput)
	}{
		{name: "invalid slug", edit: func(input *quack.TemplateInput) { input.Slug = "Invalid Slug" }},
		{name: "empty reason", edit: func(input *quack.TemplateInput) { input.ReasonTemplate = "" }},
		{name: "invalid action", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].ActionType = "explode_user" }},
		{name: "record warning action", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].ActionType = "record_warning" }},
		{name: "send dm action", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].ActionType = quack.ActionSendDM }},
		{name: "negative retry", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].MaxRetries = -1 }},
		{name: "excess retry", edit: func(input *quack.TemplateInput) {
			input.Levels[1].Actions[0].MaxRetries = quack.MaxTemplateSafeRetries + 1
		}},
		{name: "missing timeout duration", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].TimeoutDurationSeconds = 0 }},
		{name: "excess timeout duration", edit: func(input *quack.TemplateInput) {
			input.Levels[1].Actions[0].TimeoutDurationSeconds = quack.MaxTimeoutDurationSeconds + 1
		}},
		{name: "timeout ban setting", edit: func(input *quack.TemplateInput) { input.Levels[1].Actions[0].DeleteMessageSeconds = 1 }},
		{name: "excess ban history", edit: func(input *quack.TemplateInput) {
			input.Levels[1].Actions[0].ActionType = quack.ActionBanUser
			input.Levels[1].Actions[0].TimeoutDurationSeconds = 0
			input.Levels[1].Actions[0].DeleteMessageSeconds = quack.MaxBanDeleteMessageSeconds + 1
		}},
		{name: "kick setting", edit: func(input *quack.TemplateInput) {
			input.Levels[1].Actions[0].ActionType = quack.ActionKickUser
		}},
		{name: "multiple actions", edit: func(input *quack.TemplateInput) {
			input.Levels[1].Actions = append(input.Levels[1].Actions, quack.TemplateActionInput{ActionType: quack.ActionKickUser})
		}},
		{name: "no default level", edit: func(input *quack.TemplateInput) { input.Levels[0].IsDefault = false }},
		{name: "two default levels", edit: func(input *quack.TemplateInput) { input.Levels[1].IsDefault = true }},
		{name: "default level trigger", edit: func(input *quack.TemplateInput) { input.Levels[0].TriggerCaseCount = 1 }},
		{name: "escalation without trigger", edit: func(input *quack.TemplateInput) { input.Levels[1].TriggerCaseCount = 0 }},
		{name: "duplicate threshold", edit: func(input *quack.TemplateInput) {
			input.Levels = append(input.Levels, quack.TemplateLevelInput{Name: "Duplicate", TriggerCaseCount: input.Levels[1].TriggerCaseCount})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := validTemplateInput("spam-" + strings.ReplaceAll(tt.name, " ", "-"))
			tt.edit(&input)
			_, err := service.Create(ctx, guildContext, input)
			if !errors.Is(err, quack.ErrTemplateValidation) {
				t.Fatalf("expected validation error, got %v", err)
			}
		})
	}
}

func TestTemplateServiceUpdateAndArchiveAudit(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	created, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	update := validTemplateInput("spam-updated")
	update.Name = "Spam Updated"
	update.Levels[1].Actions = nil
	updated, err := service.Update(ctx, guildContext, created.ID, update)
	if err != nil {
		t.Fatalf("update template: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Fatalf("expected version increment, got %d then %d", created.Version, updated.Version)
	}
	if len(updated.Levels) != 2 || len(updated.Levels[1].Actions) != 0 {
		t.Fatalf("expected update to replace levels and actions, got %+v", updated.Levels)
	}

	archived, err := service.Archive(ctx, guildContext, created.ID)
	if err != nil {
		t.Fatalf("archive template: %v", err)
	}
	if archived.ArchivedAt == nil {
		t.Fatalf("expected archived template")
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
	service := quack.NewTemplateService(store)

	created, err := service.Create(ctx, guildOne, validTemplateInput("spam"))
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	_, err = service.Update(ctx, guildTwo, created.ID, validTemplateInput("spam"))
	if !errors.Is(err, quack.ErrTemplateNotFound) {
		t.Fatalf("expected not found across guild boundary, got %v", err)
	}
}

func TestTemplateServiceGetReturnsExplicitCompatibilityReviewError(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	// Recreate a pre-0410 quarantined fixture. The final live schema rejects
	// multiple actions before they can enter normal service behavior.
	if err := repositories.DB().Exec("DROP INDEX uq_v5_level_enforcement_action").Error; err != nil {
		t.Fatalf("drop final action constraint for compatibility fixture: %v", err)
	}
	guildContext := templateGuildContext(t, repositories, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(repositories)

	created, err := service.Create(ctx, guildContext, validTemplateInput("legacy-policy"))
	if err != nil {
		t.Fatalf("create compatibility fixture: %v", err)
	}

	var escalation store.CaseTemplateLevelRecord
	if err := repositories.DB().Where("template_id = ? AND is_default = ?", created.ID, false).First(&escalation).Error; err != nil {
		t.Fatalf("load escalation level: %v", err)
	}
	now := time.Now().UTC()
	secondAction := store.CaseTemplateLevelActionRecord{
		ULIDModelRecord:  store.ULIDModelRecord{ID: "service-compat-action00000", CreatedAt: now, UpdatedAt: now},
		LevelID:          escalation.ID,
		Position:         2,
		ActionType:       quack.ActionKickUser,
		ConfigJSON:       `{}`,
		IdempotencyScope: "case",
		Enabled:          true,
	}
	if err := repositories.DB().Select("*").Create(&secondAction).Error; err != nil {
		t.Fatalf("create preserved second action: %v", err)
	}
	if err := repositories.DB().Model(&store.CaseTemplateLevelRecord{}).Where("template_id = ? AND is_default = ?", created.ID, true).UpdateColumn("is_default", false).Error; err != nil {
		t.Fatalf("remove legacy default: %v", err)
	}
	if err := repositories.DB().Model(&store.CaseTemplateRecord{}).Where("id = ?", created.ID).UpdateColumn("archived_at", now).Error; err != nil {
		t.Fatalf("archive quarantined template: %v", err)
	}
	reason := "level has multiple actions; template does not have exactly one default level"
	if err := repositories.DB().Exec(
		"INSERT INTO quack_v5_0002_template_compatibility (template_id, previous_archived_at, previous_deleted_at, reason, recorded_at) VALUES (?, ?, ?, ?, ?)",
		created.ID, nil, nil, reason, now,
	).Error; err != nil {
		t.Fatalf("record compatibility state: %v", err)
	}

	response, err := service.Get(ctx, guildContext, created.ID)
	if response != nil {
		t.Fatalf("quarantined template returned a live response: %+v", response)
	}
	if !errors.Is(err, quack.ErrTemplateCompatibilityReviewRequired) {
		t.Fatalf("expected compatibility review error, got %v", err)
	}
	var compatibilityError *quack.TemplateCompatibilityReviewError
	if !errors.As(err, &compatibilityError) {
		t.Fatalf("expected typed compatibility error, got %T", err)
	}
	if compatibilityError.TemplateID != created.ID || compatibilityError.Reason != reason {
		t.Fatalf("unexpected compatibility error: %+v", compatibilityError)
	}
}

func TestTemplateServiceSlugUniqueness(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guildContext := templateGuildContext(t, store, "guild-1", "user-1", uint64(discordgo.PermissionManageGuild))
	service := quack.NewTemplateService(store)

	if _, err := service.Create(ctx, guildContext, validTemplateInput("spam")); err != nil {
		t.Fatalf("create template: %v", err)
	}
	_, err := service.Create(ctx, guildContext, validTemplateInput("spam"))
	if !errors.Is(err, quack.ErrTemplateValidation) {
		t.Fatalf("expected duplicate slug validation, got %v", err)
	}
}

func templateGuildContext(t *testing.T, store *store.Store, discordGuildID, userID string, permissionBits uint64) *quack.GuildStaffContext {
	t.Helper()

	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{ID: discordGuildID, Owner: permissionBits == 0, Permissions: permissionBits}},
		botGuild:   &quack.DiscordBotGuild{ID: discordGuildID, Name: "Guild", OwnerID: "owner-1"},
	})
	guildContext, err := service.ResolveStaffContext(context.Background(), testSession(userID), discordGuildID)
	if err != nil {
		t.Fatalf("resolve guild context: %v", err)
	}
	return guildContext
}

func validTemplateInput(slug string) quack.TemplateInput {
	return quack.TemplateInput{
		Slug:           slug,
		Name:           "Spam",
		Description:    "Spam template",
		ReasonTemplate: "No spam",
		Appealable:     true,
		Levels: []quack.TemplateLevelInput{
			{
				Name:       "Default",
				Position:   1,
				IsDefault:  true,
				NotifyUser: true,
			},
			{
				Name:             "Repeat spam",
				Position:         2,
				TriggerCaseCount: 3,
				Actions: []quack.TemplateActionInput{
					{
						ActionType:             quack.ActionTimeoutUser,
						TimeoutDurationSeconds: 60 * 60,
					},
				},
			},
		},
	}
}

// deniedTemplateStore records denial evidence but panics through its embedded
// nil port if an unauthorized call reaches policy persistence.
type deniedTemplateStore struct{ quack.TemplateStore }

func (deniedTemplateStore) CreateAuditLogEntry(context.Context, *quack.AuditLogEntry) error {
	return nil
}

func TestTemplateWritesRequireManagerAtServiceBoundary(t *testing.T) {
	service := quack.NewTemplateService(deniedTemplateStore{})
	staff := &quack.GuildStaffContext{Guild: &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}}, Staff: &quack.StaffMember{DiscordUserID: "moderator"}, Permissions: map[quack.PermissionAction]bool{quack.PermissionActionCaseTemplateRead: true}}
	operations := map[string]func() error{
		"create": func() error { _, err := service.Create(context.Background(), staff, quack.TemplateInput{}); return err },
		"update": func() error {
			_, err := service.Update(context.Background(), staff, "template", quack.TemplateInput{})
			return err
		},
		"archive": func() error { _, err := service.Archive(context.Background(), staff, "template"); return err },
		"restore": func() error { _, err := service.Restore(context.Background(), staff, "template"); return err },
		"export":  func() error { _, err := service.Export(context.Background(), staff, "template"); return err },
		"import": func() error {
			_, err := service.Import(context.Background(), staff, quack.TemplateImportInput{Confirm: true})
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			if err := operation(); !errors.Is(err, quack.ErrTemplatePermissionDenied) {
				t.Fatalf("expected permission denial: %v", err)
			}
		})
	}
}
