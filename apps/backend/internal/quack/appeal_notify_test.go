package quack_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// queueNotifier records rich appeal deliveries and keeps one queue post per
// appeal, like the Discord adapter.
type queueNotifier struct {
	mu        sync.Mutex
	decisions []quack.AppealDecisionNotice
	posts     int
	edits     int
	shown     []*quack.AppealResponse
}

func (n *queueNotifier) SendAppealMemberNotification(context.Context, string, string) (string, error) {
	return "", errors.New("plain member notification used")
}

func (n *queueNotifier) SendAppealStaffNotification(context.Context, string, string) (string, error) {
	return "", errors.New("plain staff notification used")
}

func (n *queueNotifier) SendAppealDecision(_ context.Context, _ string, notice quack.AppealDecisionNotice) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.decisions = append(n.decisions, notice)
	return "dm", nil
}

func (n *queueNotifier) PublishAppealQueue(_ context.Context, _ string, appeal *quack.AppealResponse, receipt quack.AppealQueueReceipt) (quack.AppealQueueReceipt, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.shown = append(n.shown, appeal)
	if receipt.MessageID != "" {
		n.edits++
		return receipt, nil
	}
	n.posts++
	return quack.AppealQueueReceipt{ChannelID: "queue", MessageID: "post"}, nil
}

func TestAppealQueuePostIsEditedInPlaceAndDecisionsCarryIntent(t *testing.T) {
	ctx := context.Background()
	store, _, _, _, created := updateFixture(t)
	admin := templateGuildContext(t, store, updateGuildDiscordID, "admin-1", uint64(discordgo.PermissionManageGuild))
	rejoin := "https://discord.com/invite/quack"
	required := true
	settings := quack.NewGuildSettingsService(store, nil, nil)
	if _, err := settings.Update(ctx, admin, quack.GuildSettingsInput{AppealRejoinURL: &rejoin, AppealReviewReasonRequired: &required}); err != nil {
		t.Fatal(err)
	}
	appeals := quack.NewAppealService(store)
	if required, err := appeals.ReviewReasonRequired(ctx, updateGuildDiscordID); err != nil || !required {
		t.Fatalf("review reason required = %v, %v", required, err)
	}
	if required, err := appeals.ReviewReasonRequired(ctx, "unknown-guild"); err != nil || required {
		t.Fatalf("unknown guild = %v, %v", required, err)
	}

	if err := appeals.CanSubmit(ctx, created.ID, "someone-else"); !errors.Is(err, quack.ErrAppealNotFound) {
		t.Fatalf("other member can submit = %v", err)
	}
	if err := appeals.CanSubmit(ctx, created.ID, "target-1"); err != nil {
		t.Fatalf("target can submit = %v", err)
	}
	answers := quack.AppealSubmissionInput{Statement: "Please reconsider."}
	appeal, err := appeals.Submit(ctx, created.ID, "target-1", answers)
	if err != nil {
		t.Fatal(err)
	}
	if appeal.CaseNumber != created.CaseNumber || appeal.TemplateName != "Spam" {
		t.Fatalf("appeal response = %+v", appeal)
	}
	if err := appeals.CanSubmit(ctx, created.ID, "target-1"); !errors.Is(err, quack.ErrAppealConflict) {
		t.Fatalf("second submission check = %v", err)
	}

	notifier := &queueNotifier{}
	dispatcher := quack.NewAppealNotificationDispatcher(store, notifier)
	if err := dispatcher.DispatchPending(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if notifier.posts != 1 || notifier.edits != 0 {
		t.Fatalf("posts=%d edits=%d", notifier.posts, notifier.edits)
	}

	reviewer := templateGuildContext(t, store, updateGuildDiscordID, "mod-2", uint64(discordgo.PermissionModerateMembers))
	// Reviewers can't read settings, so staff views carry the reason rule.
	if staffView, err := appeals.GetStaff(ctx, reviewer, appeal.ID); err != nil || !staffView.ReviewReasonRequired {
		t.Fatalf("staff appeal reason required = %+v, %v", staffView, err)
	}
	if queue, err := appeals.ListStaff(ctx, reviewer, quack.AppealStatusPending, 10, 0); err != nil || !queue.ReviewReasonRequired {
		t.Fatalf("staff queue reason required = %+v, %v", queue, err)
	}
	if appeal.ReviewReasonRequired {
		t.Fatal("member appeal response carries the staff reason rule")
	}
	asked, err := appeals.RequestInformation(ctx, reviewer, appeal.ID, "Which message?")
	if err != nil {
		t.Fatal(err)
	}
	if !asked.ReviewReasonRequired {
		t.Fatal("decision response lost the reason rule")
	}
	if _, err := appeals.SubmitInformation(ctx, appeal.ID, "target-1", quack.AppealInformationInput{Body: "The first one."}); err != nil {
		t.Fatal(err)
	}
	if _, err := appeals.Accept(ctx, reviewer, appeal.ID, "Fair point."); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.DispatchPending(ctx, 10); err != nil {
		t.Fatal(err)
	}
	if notifier.posts != 1 || notifier.edits == 0 {
		t.Fatalf("queue post was not edited in place: posts=%d edits=%d", notifier.posts, notifier.edits)
	}
	if last := notifier.shown[len(notifier.shown)-1]; last.Status != quack.AppealStatusAccepted {
		t.Fatalf("queue post shows %s, want accepted", last.Status)
	}
	if len(notifier.decisions) != 2 {
		t.Fatalf("decisions = %+v", notifier.decisions)
	}
	accepted := notifier.decisions[1].Intent
	if accepted.Status != quack.AppealStatusAccepted || accepted.Reason != "Fair point." || accepted.GuildName != "Guild" ||
		accepted.CaseNumber != created.CaseNumber || accepted.RejoinURL != "https://discord.gg/quack" {
		t.Fatalf("accepted intent = %+v", accepted)
	}
	if asked := notifier.decisions[0].Intent; asked.Status != quack.AppealStatusNeedsInformation || asked.RejoinURL != "" {
		t.Fatalf("information intent = %+v", asked)
	}

	// The member's DM never names the reviewer.
	body, _ := json.Marshal(notifier.decisions)
	if strings.Contains(string(body), "mod-2") {
		t.Fatalf("decision notice names the reviewer: %s", body)
	}
}

func TestAppealSettingsValidateRejoinURLAndQueueChannel(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	admin := templateGuildContext(t, store, "guild-1", "admin-1", uint64(discordgo.PermissionManageGuild))
	if err := insertGuildSettings(store, quack.GuildSettings{GuildID: admin.Guild.ID}); err != nil {
		t.Fatal(err)
	}
	service := quack.NewGuildSettingsService(store, nil, nil)
	for _, bad := range []string{"http://discord.gg/abc", "https://example.com/abc", "https://discord.gg/abc?x=1", "https://discord.com/channels/1/2", "https://user@discord.gg/abc"} {
		if _, err := service.Update(ctx, admin, quack.GuildSettingsInput{AppealRejoinURL: &bad}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
			t.Fatalf("rejoin url %q = %v", bad, err)
		}
	}
	good := " https://discord.com/invite/Quack-1 "
	updated, err := service.Update(ctx, admin, quack.GuildSettingsInput{AppealRejoinURL: &good})
	if err != nil || updated.AppealRejoinURL != "https://discord.gg/Quack-1" {
		t.Fatalf("rejoin url = %+v, %v", updated, err)
	}
	channel := "123456789012345678"
	if _, err := service.Update(ctx, admin, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &channel}); !errors.Is(err, quack.ErrGuildSettingsValidation) {
		t.Fatalf("queue channel without validator = %v", err)
	}
	validated := quack.NewGuildSettingsService(store, allowStaffChannel{}, nil)
	updated, err = validated.Update(ctx, admin, quack.GuildSettingsInput{AppealQueueChannelDiscordID: &channel})
	if err != nil || updated.AppealQueueChannelDiscordID != channel || updated.AppealRejoinURL != "https://discord.gg/Quack-1" {
		t.Fatalf("queue channel = %+v, %v", updated, err)
	}
}
