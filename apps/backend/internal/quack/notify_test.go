package quack_test

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestActionServiceDoesNotAutomaticallyRetryNotificationFailure(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	adminContext := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	modContext := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))

	template := createAppTemplate(t, ctx, store, adminContext, validTemplateInput("retry-dm"))
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, modContext, quack.CaseInput{
		TemplateID:          template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	fakeDiscord := &fakeActionClient{dmFailures: []error{
		quack.DiscordError{Code: "rate_limited", Message: "rate limited", Retryable: true},
		nil,
	}}
	if err := quack.NewActionService(store, nil, fakeDiscord, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process first attempt: %v", err)
	}
	notification, err := store.GetCaseNotification(ctx, created.ID)
	if err != nil || notification == nil || notification.Status != quack.NotificationFailed {
		t.Fatalf("expected terminal notification failure, got %+v err=%v", notification, err)
	}
	if err := quack.NewActionService(store, nil, fakeDiscord, nil, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatalf("process duplicate request: %v", err)
	}
	if len(fakeDiscord.dms) != 0 {
		t.Fatalf("notification retry sent a duplicate: %+v", fakeDiscord.dms)
	}
}

func TestAppealableCaseNotificationUsesSecureDashboardControlContract(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template := createAppTemplate(t, ctx, store, admin, validTemplateInput("appeal-link"))
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target-1"})
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeEnforcementClient{}
	if err := quack.NewActionService(store, client, client, nil, nil, "https://dashboard.example").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "send_case_notification" || client.dashboardBaseURL != "https://dashboard.example" || client.notificationGuildID != moderator.Guild.ID || client.notificationCaseID != created.ID {
		t.Fatalf("secure appeal notification contract was not used: %+v", client)
	}
}
