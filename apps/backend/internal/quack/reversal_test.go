package quack_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

// richNotifier is a messenger that renders case DMs itself.
type richNotifier struct {
	fakeEnforcementClient
	requests []quack.CaseNotificationRequest
}

func (n *richNotifier) DeliverCaseNotification(_ context.Context, request quack.CaseNotificationRequest) (quack.CaseNotificationReceipt, error) {
	n.requests = append(n.requests, request)
	return quack.CaseNotificationReceipt{RenderedMessage: "rendered", MessageID: "dm-1"}, nil
}

// timeoutCase creates a case whose default level times the member out for
// an hour, and runs its enforcement.
func timeoutCase(t *testing.T, store *storage.Store, slug, target string, client quack.Enforcer, messenger quack.Messenger) (*quack.CaseResponse, *quack.GuildStaffContext) {
	t.Helper()
	ctx := context.Background()
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	moderator := templateGuildContext(t, store, "guild-1", "mod-1", uint64(discordgo.PermissionModerateMembers))
	template, err := quack.NewTemplateService(store).Create(ctx, admin,
		actionTemplateInput(slug, []quack.TemplateActionInput{{ActionType: quack.ActionTimeoutUser, TimeoutDurationSeconds: 3600}}))
	if err != nil {
		t.Fatal(err)
	}
	created, err := quack.NewCaseService(store, nil, nil, nil).Create(ctx, moderator, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: target})
	if err != nil {
		t.Fatal(err)
	}
	if err := quack.NewActionService(store, client, messenger, nil, nil, "https://dash.example").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	return created, moderator
}

func TestRichCaseNotificationCarriesOutcomeFacts(t *testing.T) {
	store := newMigratedStore(t)
	client := &timeoutUntilClient{}
	notifier := &richNotifier{}
	created, _ := timeoutCase(t, store, "rich", "target-1", client, notifier)
	if len(notifier.requests) != 1 {
		t.Fatalf("requests = %+v", notifier.requests)
	}
	request := notifier.requests[0]
	if request.CaseNumber != created.CaseNumber || request.RuleName != "Spam" || request.Reason != "No spam" || request.GuildName != "Guild" ||
		!request.Appealable || !strings.HasPrefix(request.AppealURL, "https://dash.example/guilds/") || len(request.Outcomes) != 1 {
		t.Fatalf("request = %+v", request)
	}
	outcome := request.Outcomes[0]
	if outcome.ActionType != quack.ActionTimeoutUser || outcome.Status != quack.ActionExecutionSucceeded ||
		outcome.TimeoutUntil == nil || !outcome.TimeoutUntil.Equal(client.until) {
		t.Fatalf("outcome = %+v", outcome)
	}
	notification, err := store.GetCaseNotification(context.Background(), created.ID)
	if err != nil || notification.Status != quack.NotificationSent || notification.RenderedMessage != "rendered" || notification.DeliveryMessageDiscordID != "dm-1" {
		t.Fatalf("notification = %+v err=%v", notification, err)
	}
}

// timeoutUntilClient reports when its timeouts end, as Discord does.
type timeoutUntilClient struct {
	fakeEnforcementClient
	until time.Time
}

func (c *timeoutUntilClient) TimeoutMember(ctx context.Context, guildID, userID string, seconds int, reason string) (map[string]any, error) {
	_, _ = c.fakeEnforcementClient.TimeoutMember(ctx, guildID, userID, seconds, reason)
	c.until = time.Now().UTC().Add(time.Duration(seconds) * time.Second).Truncate(time.Second)
	return map[string]any{"timeout_until": c.until.Format(time.RFC3339)}, nil
}

func TestVoidingReversesSucceededPunishmentsWithVoiderPermission(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	client := &fakeEnforcementClient{}
	created, moderator := timeoutCase(t, store, "voided", "target-1", client, client)
	if _, err := quack.NewCaseService(store, nil, nil, nil).Void(ctx, moderator, created.ID, "wrong member"); err != nil {
		t.Fatal(err)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.ID)
	if err != nil || len(actions) != 2 || actions[1].ActionType != quack.ActionRemoveTimeout || actions[1].Status != quack.ActionExecutionPending {
		t.Fatalf("actions after void = %+v err=%v", actions, err)
	}

	// The voider cannot moderate members any more, so the reversal waits
	// for staff.
	denied := quack.NewGuildService(store, fakeDiscordClient{authorization: &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "guild-1", OwnerID: "owner-1"},
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: "mod-1", Present: true},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 100, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target-1", Present: true, TopRolePosition: 1},
	}})
	client.calls = nil
	if err := quack.NewActionService(store, client, client, denied, nil, "").ProcessCaseActions(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	actions, _ = store.ListCaseActionExecutions(ctx, created.ID)
	if actions[1].Status != quack.ActionExecutionFailed || actions[1].LastErrorCode != "reversal_permission_denied" || len(client.calls) != 0 {
		t.Fatalf("denied reversal = %+v calls=%v", actions[1], client.calls)
	}
}

func TestAutomaticReversalRunsWhenAllowedAndStopsOnCompetingPunishment(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	client := &fakeEnforcementClient{}
	allowed := quack.NewGuildService(store, fakeDiscordClient{authorization: &quack.DiscordGuildAuthorization{
		Guild:  quack.DiscordBotGuild{ID: "guild-1", OwnerID: "owner-1"},
		Actor:  quack.DiscordMemberAuthorization{DiscordUserID: "mod-1", Present: true, PermissionBits: uint64(discordgo.PermissionAdministrator), TopRolePosition: 10},
		Bot:    quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 100, Bot: true},
		Target: &quack.DiscordMemberAuthorization{DiscordUserID: "target-1", Present: true, TopRolePosition: 1},
	}})
	cases := quack.NewCaseService(store, nil, nil, nil)

	first, moderator := timeoutCase(t, store, "first", "target-1", client, client)
	if _, err := cases.Void(ctx, moderator, first.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	client.calls = nil
	if err := quack.NewActionService(store, client, client, allowed, nil, "").ProcessCaseActions(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(client.calls, ",") != "remove_timeout" {
		t.Fatalf("calls = %v, want remove_timeout", client.calls)
	}

	// A later timeout from another case must not be lifted by voiding an
	// earlier one.
	earlier, _ := timeoutCase(t, store, "earlier", "target-2", client, client)
	timeoutCase(t, store, "later", "target-2", client, client)
	if _, err := cases.Void(ctx, moderator, earlier.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	client.calls = nil
	if err := quack.NewActionService(store, client, client, allowed, nil, "").ProcessCaseActions(ctx, earlier.ID); err != nil {
		t.Fatal(err)
	}
	actions, _ := store.ListCaseActionExecutions(ctx, earlier.ID)
	if len(client.calls) != 0 || actions[1].Status != quack.ActionExecutionFailed || actions[1].LastErrorCode != "reversal_ownership_conflict" {
		t.Fatalf("competing reversal = %+v calls=%v", actions[1], client.calls)
	}
}
