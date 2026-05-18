package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

type fakeActionClient struct {
	dmFailures  []error
	logFailures []error
	dms         []fakeActionMessage
	logs        []fakeActionMessage
}

type fakeActionMessage struct {
	TargetID string
	Message  string
}

func (f *fakeActionClient) SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error) {
	_ = ctx
	if len(f.dmFailures) > 0 {
		err := f.dmFailures[0]
		f.dmFailures = f.dmFailures[1:]
		if err != nil {
			return nil, err
		}
	}
	f.dms = append(f.dms, fakeActionMessage{TargetID: discordUserID, Message: message})
	return map[string]any{"message_id": "dm-message-1"}, nil
}

func (f *fakeActionClient) SendModLog(ctx context.Context, discordChannelID, message string) (map[string]any, error) {
	_ = ctx
	if len(f.logFailures) > 0 {
		err := f.logFailures[0]
		f.logFailures = f.logFailures[1:]
		if err != nil {
			return nil, err
		}
	}
	f.logs = append(f.logs, fakeActionMessage{TargetID: discordChannelID, Message: message})
	return map[string]any{"message_id": "log-message-1"}, nil
}

func TestActionServiceProcessesSafeActions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	setModLogChannel(t, store, modContext.Guild.ID, "mod-log-1")

	template := createAppTemplate(t, ctx, store, adminContext, actionTemplateInput("safe-actions", []app.TemplateActionInput{
		{ActionType: structs.ActionRecordWarning, Config: json.RawMessage(`{}`), IdempotencyScope: "case"},
		{ActionType: structs.ActionSendDM, Config: json.RawMessage(`{"message":"Please stop"}`), IdempotencyScope: "case"},
		{ActionType: structs.ActionWriteModLog, Config: json.RawMessage(`{}`), IdempotencyScope: "case"},
	}))
	created, err := app.NewCaseService(store).Create(ctx, modContext, app.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{}
	if err := app.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}

	if len(fakeDiscord.dms) != 1 || fakeDiscord.dms[0].TargetID != "target-1" || fakeDiscord.dms[0].Message != "Please stop" {
		t.Fatalf("unexpected DMs: %+v", fakeDiscord.dms)
	}
	if len(fakeDiscord.logs) != 1 || fakeDiscord.logs[0].TargetID != "mod-log-1" {
		t.Fatalf("unexpected mod logs: %+v", fakeDiscord.logs)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	for _, action := range actions {
		if action.Status != structs.ActionExecutionSucceeded {
			t.Fatalf("expected all actions succeeded, got %+v", actions)
		}
	}
	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if cases[0].Status != structs.CaseStatusCompleted {
		t.Fatalf("expected completed case, got %+v", cases[0])
	}
}

func TestActionServiceRetriesTransientFailure(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	template := createAppTemplate(t, ctx, store, adminContext, actionTemplateInput("retry-dm", []app.TemplateActionInput{
		{
			ActionType:       structs.ActionSendDM,
			Config:           json.RawMessage(`{"message":"retry me"}`),
			MaxRetries:       1,
			RetryBackoffMS:   1000,
			IdempotencyScope: "case",
		},
	}))
	created, err := app.NewCaseService(store).Create(ctx, modContext, app.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{dmFailures: []error{
		app.DiscordActionError{Code: "rate_limited", Message: "rate limited", Retryable: true},
		nil,
	}}
	if err := app.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process first attempt: %v", err)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != structs.ActionExecutionRetrying || actions[0].NextRetryAt == nil {
		t.Fatalf("expected retrying action, got %+v", actions[0])
	}

	past := time.Now().UTC().Add(-time.Minute)
	if err := store.DB().Model(&structs.CaseActionExecution{}).Where("id = ?", actions[0].ID).Update("next_retry_at", past).Error; err != nil {
		t.Fatalf("make retry eligible: %v", err)
	}
	if err := app.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process second attempt: %v", err)
	}
	actions, err = store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != structs.ActionExecutionSucceeded || actions[0].AttemptCount != 2 {
		t.Fatalf("expected successful retry, got %+v", actions[0])
	}
}

func TestActionServiceFailureSkipsLaterActionsUnlessContinueOnError(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	template := createAppTemplate(t, ctx, store, adminContext, actionTemplateInput("stop-on-error", []app.TemplateActionInput{
		{ActionType: structs.ActionSendDM, Config: json.RawMessage(`{"message":"blocked"}`), IdempotencyScope: "case"},
		{ActionType: structs.ActionRecordWarning, Config: json.RawMessage(`{}`), IdempotencyScope: "case"},
	}))
	created, err := app.NewCaseService(store).Create(ctx, modContext, app.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	fakeDiscord := &fakeActionClient{dmFailures: []error{
		app.DiscordActionError{Code: "dm_closed", Message: "closed DMs", Retryable: false},
	}}
	if err := app.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != structs.ActionExecutionFailed || actions[1].Status != structs.ActionExecutionSkipped {
		t.Fatalf("expected failure to skip later action, got %+v", actions)
	}

	continueInput := actionTemplateInput("continue-on-error", []app.TemplateActionInput{
		{ActionType: structs.ActionSendDM, Config: json.RawMessage(`{"message":"blocked"}`), ContinueOnError: true, IdempotencyScope: "case"},
		{ActionType: structs.ActionRecordWarning, Config: json.RawMessage(`{}`), IdempotencyScope: "case"},
	})
	continueTemplate := createAppTemplate(t, ctx, store, adminContext, continueInput)
	continued, err := app.NewCaseService(store).Create(ctx, modContext, app.CaseInput{
		TemplateID:          continueTemplate.ID,
		TargetDiscordUserID: "target-2",
	})
	if err != nil {
		t.Fatalf("create continuing case: %v", err)
	}
	fakeDiscord = &fakeActionClient{dmFailures: []error{
		app.DiscordActionError{Code: "dm_closed", Message: "closed DMs", Retryable: false},
	}}
	if err := app.NewActionService(store, fakeDiscord).ProcessCaseActions(ctx, continued.ID); err != nil {
		t.Fatalf("process continuing actions: %v", err)
	}
	actions, err = store.ListCaseActionExecutions(ctx, continued.ID)
	if err != nil {
		t.Fatalf("list continuing actions: %v", err)
	}
	if actions[0].Status != structs.ActionExecutionFailed || actions[1].Status != structs.ActionExecutionSucceeded {
		t.Fatalf("expected continue_on_error to run later action, got %+v", actions)
	}
}

func setModLogChannel(t *testing.T, store *storage.Store, guildID, channelID string) {
	t.Helper()

	settings, err := store.EnsureGuildSettings(context.Background(), guildID)
	if err != nil {
		t.Fatalf("ensure guild settings: %v", err)
	}
	settings.ModLogChannelDiscordID = channelID
	if err := store.DB().Save(settings).Error; err != nil {
		t.Fatalf("save guild settings: %v", err)
	}
}

func actionTemplateInput(slug string, actions []app.TemplateActionInput) app.TemplateInput {
	input := validTemplateInput(slug)
	input.Actions = actions
	input.EscalationRules = nil
	return input
}
