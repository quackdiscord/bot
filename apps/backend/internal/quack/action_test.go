package quack_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

type fakeActionClient struct {
	dmFailures []error
	dms        []fakeActionMessage
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

// PrepareDM fails so notifications fall back to SendDM.
func (f *fakeActionClient) PrepareDM(context.Context, string) (string, error) {
	return "", errors.New("prepared DMs are not supported by this fake")
}

func (f *fakeActionClient) SendPreparedDM(ctx context.Context, channelID, message string) (map[string]any, error) {
	return f.SendDM(ctx, channelID, message)
}

func (f *fakeActionClient) SendCaseNotification(ctx context.Context, discordUserID, _, message, _, _, _ string) (map[string]any, error) {
	return f.SendDM(ctx, discordUserID, message)
}

func TestActionServiceProcessesSafeActions(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("safe-actions"))
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{}
	if err := quack.NewActionService(store, nil, fakeDiscord, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}

	if len(fakeDiscord.dms) != 1 || fakeDiscord.dms[0].TargetID != "target-1" || !strings.Contains(fakeDiscord.dms[0].Message, "Reason: No spam") {
		t.Fatalf("unexpected DMs: %+v", fakeDiscord.dms)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if len(actions) != 0 {
		t.Fatalf("expected no enforcement action for the default warning, got %+v", actions)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != quack.NotificationSent {
		t.Fatalf("expected sent case notification, got %+v err=%v", notification, err)
	}
	cases, err := store.ListCases(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if cases[0].Validity != quack.CaseValidityValid {
		t.Fatalf("expected action completion not to change case validity, got %+v", cases[0])
	}
}

func TestActionServiceDoesNotNotifyForUnsupportedAction(t *testing.T) {
	ctx := quack.ContextWithTrace(context.Background(), "req-action-1", "corr-action-1")
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	unsupportedInput := actionTemplateInput("unsupported-notify", []quack.TemplateActionInput{
		{ActionType: quack.ActionBanUser},
	})
	unsupportedInput.Levels[0].NotifyUser = false
	template := createAppTemplate(t, ctx, store, adminContext, unsupportedInput)
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	fakeDiscord := &fakeActionClient{}
	if err := quack.NewActionService(store, nil, fakeDiscord, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process actions: %v", err)
	}
	if len(fakeDiscord.dms) != 0 {
		t.Fatalf("did not expect DM before unsupported ban action, got %+v", fakeDiscord.dms)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != quack.ActionExecutionFailed {
		t.Fatalf("expected unsupported action to fail, got %+v", actions[0])
	}
	if actions[0].LastErrorCode != "discord_unavailable" || actions[0].NextRetryAt != nil {
		t.Fatalf("expected visible non-retryable unsupported action, got %+v", actions[0])
	}
	attempts, err := store.ListCaseActionAttempts(ctx, []string{actions[0].ID})
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].ErrorCode != "discord_unavailable" {
		t.Fatalf("expected failed unsupported attempt, got %+v", attempts)
	}
	audits, err := store.ListAuditLogEntries(ctx, modContext.Guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	var failureAudit *quack.AuditLogEntry
	for i := range audits {
		if audits[i].Action == "case_action.failed" {
			failureAudit = &audits[i]
			break
		}
	}
	if failureAudit == nil || failureAudit.RequestID != "req-action-1" || failureAudit.CorrelationID != "corr-action-1" {
		t.Fatalf("expected traced action failure audit, got %+v", audits)
	}
}

func TestActionServiceReversal(t *testing.T) {
	missingAppeal := "missing-appeal"
	tests := []struct {
		name           string
		originalStatus quack.ActionExecutionStatus
		reversal       quack.ActionType
		appealID       *string
		wantErr        string
	}{
		{name: "timeout removal by case number", originalStatus: quack.ActionExecutionSucceeded, reversal: quack.ActionRemoveTimeout},
		{name: "original not succeeded", originalStatus: quack.ActionExecutionFailed, reversal: quack.ActionRemoveTimeout, wantErr: "only a succeeded action"},
		{name: "reversal does not match", originalStatus: quack.ActionExecutionSucceeded, reversal: quack.ActionUnbanUser, wantErr: "does not match"},
		{name: "appeal not accepted", originalStatus: quack.ActionExecutionSucceeded, reversal: quack.ActionRemoveTimeout, appealID: &missingAppeal, wantErr: "not accepted"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store := newMigratedStore(t)
			admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
			moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
			template := createAppTemplate(t, ctx, store, admin, actionTemplateInput("numbered-reversal", []quack.TemplateActionInput{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 60}}))
			created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
			if err != nil {
				t.Fatalf("create reversal case: %v", err)
			}
			actions, err := store.ListCaseActionExecutions(ctx, created.ID)
			if err != nil || len(actions) != 1 {
				t.Fatalf("load original action: actions=%+v err=%v", actions, err)
			}
			if err := store.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", actions[0].ID).Update("status", tt.originalStatus).Error; err != nil {
				t.Fatalf("set original action status: %v", err)
			}
			authorizer := quack.NewGuildService(store, fakeDiscordClient{authorization: &quack.DiscordGuildAuthorization{
				Guild:  quack.DiscordBotGuild{ID: "guild-1", OwnerID: "owner-1"},
				Actor:  quack.DiscordMemberAuthorization{DiscordUserID: "mod-1", Present: true, PermissionBits: uint64(discordgo.PermissionAdministrator), TopRolePosition: 10},
				Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 100, Bot: true},
				Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target-1", Present: true, TopRolePosition: 1},
			}})
			reversal, err := quack.NewActionService(store, nil, nil, authorizer, nil, "").
				ReverseForAppeal(ctx, moderator, strconv.FormatUint(created.CaseNumber, 10), actions[0].ID, tt.reversal, tt.appealID)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ReverseForAppeal() err = %v, want %q", err, tt.wantErr)
				}
				after, _ := store.ListCaseActionExecutions(ctx, created.ID)
				if len(after) != 1 {
					t.Fatalf("rejected reversal was queued: %+v", after)
				}
				return
			}
			if err != nil || reversal == nil {
				t.Fatalf("reverse case by number: reversal=%+v err=%v", reversal, err)
			}
			if reversal.CaseID != created.ID || reversal.ActionType != tt.reversal || reversal.SafeForRetry {
				t.Fatalf("unexpected reversal: %+v", reversal)
			}
		})
	}
}

func actionTemplateInput(slug string, actions []quack.TemplateActionInput) quack.TemplateInput {
	input := validTemplateInput(slug)
	input.Levels[0].Actions = actions
	return input
}

type fakeEnforcementClient struct {
	calls                   []string
	duration, deleteSeconds int
	reason                  string
	dashboardBaseURL        string
	notificationGuildID     string
	notificationCaseID      string
}

func (f *fakeEnforcementClient) SendDM(context.Context, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "send_dm")
	return map[string]any{"message_id": "dm"}, nil
}

func (f *fakeEnforcementClient) PrepareDM(context.Context, string) (string, error) {
	f.calls = append(f.calls, "prepare_dm")
	return "dm-channel", nil
}

func (f *fakeEnforcementClient) SendPreparedDM(context.Context, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "send_prepared_dm")
	return map[string]any{"message_id": "dm"}, nil
}

func (f *fakeEnforcementClient) SendCaseNotification(_ context.Context, _, _ string, _ string, dashboardBaseURL, guildID, caseID string) (map[string]any, error) {
	f.calls = append(f.calls, "send_case_notification")
	f.dashboardBaseURL, f.notificationGuildID, f.notificationCaseID = dashboardBaseURL, guildID, caseID
	return map[string]any{"message_id": "dm"}, nil
}

func (f *fakeEnforcementClient) TimeoutMember(_ context.Context, _, _ string, duration int, reason string) (map[string]any, error) {
	f.calls = append(f.calls, "timeout")
	f.duration = duration
	f.reason = reason
	return map[string]any{"ok": true}, nil
}

func (f *fakeEnforcementClient) KickMember(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "kick")
	return map[string]any{"ok": true}, nil
}

func (f *fakeEnforcementClient) BanMember(_ context.Context, _, _ string, seconds int, reason string) (map[string]any, error) {
	f.calls = append(f.calls, "ban")
	f.deleteSeconds = seconds
	f.reason = reason
	return map[string]any{"ok": true}, nil
}

func (f *fakeEnforcementClient) RemoveMemberTimeout(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "remove_timeout")
	return map[string]any{"ok": true}, nil
}

func (f *fakeEnforcementClient) UnbanMember(context.Context, string, string, string) (map[string]any, error) {
	f.calls = append(f.calls, "unban")
	return map[string]any{"ok": true}, nil
}

func TestEnforcementUsesExactSettingsAndNotificationOrder(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("timeout-policy")
	input.Levels[0].Actions = []quack.TemplateActionInput{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 937, MaxRetries: 2}}
	template := createAppTemplate(t, ctx, store, admin, input)
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client, client, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "timeout,send_dm" || client.duration != 937 || !strings.Contains(client.reason, "case #1") || !strings.Contains(client.reason, "No spam") {
		t.Fatalf("unexpected execution calls/settings: calls=%v duration=%d reason=%q", client.calls, client.duration, client.reason)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(actions) != 1 || actions[0].Status != quack.ActionExecutionSucceeded {
		t.Fatalf("action did not succeed: %+v err=%v", actions, err)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != quack.NotificationSent {
		t.Fatalf("notification not sent after outcome: %+v err=%v", notification, err)
	}
}

func TestBanPreparesDMAndUsesExactHistoryDeletion(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	input := validTemplateInput("ban-policy")
	input.Levels[0].Actions = []quack.TemplateActionInput{{ActionType: quack.ActionBanUser, DeleteMessageSeconds: 86400}}
	template := createAppTemplate(t, ctx, store, admin, input)
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client, client, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "prepare_dm,ban,send_prepared_dm" || client.deleteSeconds != 86400 {
		t.Fatalf("ban/notification order or setting mismatch: calls=%v delete=%d", client.calls, client.deleteSeconds)
	}
}
