package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestActionClaimIsSingleWinnerUnderConcurrency(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []quack.CaseActionExecution{{ActionType: quack.ActionTimeoutUser, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, claimErr := repository.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
			if claimErr == nil && claimed != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("got %d claim winners, want 1", winners.Load())
	}
}

func TestActionRecoveryControlsAreIdempotentAndAuditable(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	created, err := repository.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []quack.CaseActionExecution{{ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded, ConfigSnapshotJSON: `{}`}, {Position: 1, ActionType: quack.ActionKickUser, Status: quack.ActionExecutionFailed, ConfigSnapshotJSON: `{}`}}})
	if err != nil {
		t.Fatal(err)
	}
	actions, err := repository.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatal(err)
	}
	failed := actions[1]
	retryParams := quack.RetryCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	firstRetry, err := repository.RetryCaseAction(ctx, retryParams)
	if err != nil {
		t.Fatal(err)
	}
	secondRetry, err := repository.RetryCaseAction(ctx, retryParams)
	if err != nil || secondRetry.ID != firstRetry.ID {
		t.Fatalf("retry was not idempotent: %+v err=%v", secondRetry, err)
	}
	if err := repository.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", failed.ID).Update("status", quack.ActionExecutionFailed).Error; err != nil {
		t.Fatal(err)
	}
	dismissParams := quack.DismissCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	firstDismiss, err := repository.DismissCaseAction(ctx, dismissParams)
	if err != nil {
		t.Fatal(err)
	}
	secondDismiss, err := repository.DismissCaseAction(ctx, dismissParams)
	if err != nil || secondDismiss.ID != firstDismiss.ID {
		t.Fatalf("dismiss was not idempotent: %+v err=%v", secondDismiss, err)
	}
	reversalParams := quack.QueueCaseReversalParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod", OriginalExecutionID: actions[0].ID, ActionType: quack.ActionRemoveTimeout}
	firstReversal, err := repository.QueueCaseReversal(ctx, reversalParams)
	if err != nil {
		t.Fatal(err)
	}
	secondReversal, err := repository.QueueCaseReversal(ctx, reversalParams)
	if err != nil || secondReversal.ID != firstReversal.ID {
		t.Fatalf("reversal was not idempotent: first=%+v second=%+v err=%v", firstReversal, secondReversal, err)
	}
}

func TestNotificationClaimRecoversBeforeSendButNeverRepeatsAmbiguousSend(t *testing.T) {
	ctx := context.Background()
	repository, guildID := templateTestStore(t)
	notification := &quack.CaseNotification{Status: quack.NotificationPending}
	created, err := repository.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), Notification: notification})
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.ClaimCaseNotification(ctx, quack.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil || first == nil || first.Status != quack.NotificationClaimed {
		t.Fatalf("first claim: %+v err=%v", first, err)
	}
	expired := time.Now().UTC().Add(-time.Minute)
	if err := repository.DB().Model(&quack.CaseNotification{}).Where("id = ?", first.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	second, err := repository.ClaimCaseNotification(ctx, quack.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-2"})
	if err != nil || second == nil || second.LeaseToken == first.LeaseToken {
		t.Fatalf("safe pre-send recovery failed: %+v err=%v", second, err)
	}
	if err := repository.BeginCaseNotificationDelivery(ctx, second.ID, second.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err := repository.DB().Model(&quack.CaseNotification{}).Where("id = ?", second.ID).Update("lease_expires_at", expired).Error; err != nil {
		t.Fatal(err)
	}
	third, err := repository.ClaimCaseNotification(ctx, quack.ClaimCaseNotificationParams{CaseID: created.Case.ID, WorkerID: "worker-3"})
	if err != nil || third != nil {
		t.Fatalf("ambiguous send was automatically repeated: %+v err=%v", third, err)
	}
}

func TestExpiredLeaseRequiresReview(t *testing.T) {
	for _, action := range []quack.CaseActionExecution{
		{ActionType: quack.ActionBanUser, SafeForRetry: true, MaxRetries: 3},
		{ActionType: quack.ActionKickUser, MaxRetries: 3},
		{ActionType: quack.ActionTimeoutUser, SafeForRetry: true, MaxRetries: 0},
	} {
		t.Run(string(action.ActionType), func(t *testing.T) {
			ctx := context.Background()
			repository, guildID := templateTestStore(t)
			action.ConfigSnapshotJSON = `{}`
			created, err := repository.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent(), ActionExecutions: []quack.CaseActionExecution{action}})
			if err != nil {
				t.Fatal(err)
			}
			first, err := repository.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "old"})
			if err != nil || first == nil {
				t.Fatalf("claim: %+v %v", first, err)
			}
			if err := repository.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", first.Execution.ID).Update("lease_expires_at", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			second, err := repository.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "new"})
			if err != nil || second != nil {
				t.Fatalf("unsafe repeat: %+v %v", second, err)
			}
			current, err := repository.GetCaseActionExecution(ctx, guildID, first.Execution.ID)
			if err != nil || current.Status != quack.ActionExecutionFailed || current.AttemptCount != 1 || current.LeaseToken != "" {
				t.Fatalf("review state: %+v %v", current, err)
			}
			err = repository.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken, AttemptNumber: 1, WorkerID: "old", AttemptStatus: quack.ActionAttemptSucceeded, ExecutionStatus: quack.ActionExecutionSucceeded})
			if err == nil {
				t.Fatal("expired worker overwrote review state")
			}
		})
	}
}
