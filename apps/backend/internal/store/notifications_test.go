package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestNotificationWaitsForEnforcement(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), pendingNotification())
	params := quack.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker"}
	if got, err := s.ClaimCaseNotification(ctx, params); err != nil || got != nil {
		t.Fatalf("claimed before enforcement = %+v, %v", got, err)
	}
	if err := s.PrepareCaseNotification(ctx, created.Case.ID, "dm-channel", ""); err != nil {
		t.Fatal(err)
	}
	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionFailed)
	got, err := s.ClaimCaseNotification(ctx, params)
	if err != nil || got == nil || got.PreparedChannelDiscordID != "dm-channel" || got.AttemptCount != 1 {
		t.Fatalf("claim after enforcement = %+v, %v", got, err)
	}
}

// TestNotificationNeverResendsAfterSendBegins checks the two halves of DM
// recovery: a claim whose worker died before sending is reclaimed, but once
// sending began it never is, because the DM may have gone out.
func TestNotificationNeverResendsAfterSendBegins(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, nil, pendingNotification())
	params := quack.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker"}
	first, err := s.ClaimCaseNotification(ctx, params)
	if err != nil || first == nil || first.Status != quack.NotificationClaimed {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	expire(t, s, "case_notifications", first.ID)
	second, err := s.ClaimCaseNotification(ctx, params)
	if err != nil || second == nil || second.LeaseToken == first.LeaseToken {
		t.Fatalf("reclaim before send = %+v, %v", second, err)
	}
	if err := s.BeginCaseNotificationDelivery(ctx, first.ID, first.LeaseToken); err == nil {
		t.Fatal("stale worker began delivery")
	}
	if err := s.BeginCaseNotificationDelivery(ctx, second.ID, second.LeaseToken); err != nil {
		t.Fatal(err)
	}
	expire(t, s, "case_notifications", second.ID)
	if third, err := s.ClaimCaseNotification(ctx, params); err != nil || third != nil {
		t.Fatalf("reclaimed a notification mid-send: %+v, %v", third, err)
	}

	result := quack.CompleteCaseNotificationParams{NotificationID: second.ID, LeaseToken: second.LeaseToken, WorkerID: "worker",
		Status: quack.NotificationSent, RenderedMessage: "You were warned", DeliveryMessageDiscordID: "message",
		EventType: quack.CaseEventNotificationSent}
	if err := s.CompleteCaseNotification(ctx, result); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteCaseNotification(ctx, result); err == nil {
		t.Fatal("completed a notification twice")
	}
	sent, err := s.GetCaseNotification(ctx, created.Case.ID)
	if err != nil || sent.Status != quack.NotificationSent || sent.SentAt == nil || sent.LeaseToken != "" {
		t.Fatalf("sent notification = %+v, %v", sent, err)
	}
	audits, err := s.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: "case_notification.sent"})
	if err != nil || audits.Total != 1 {
		t.Fatalf("send audits = %+v, %v", audits, err)
	}
}
