package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestReviewControlsAreIdempotent(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, []quack.CaseActionExecution{
		{Position: 0, ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionSucceeded},
		{Position: 1, ActionType: quack.ActionKickUser, Status: quack.ActionExecutionFailed, LastErrorCode: "missing_permissions"},
	}, nil)
	succeeded, failed := created.ActionExecutions[0], created.ActionExecutions[1]

	queue, err := s.ListFailedCaseActions(ctx, quack.FailedCaseActionFilter{GuildID: guildID})
	if err != nil || queue.Total != 1 || queue.Executions[0].ID != failed.ID {
		t.Fatalf("review queue = %+v, %v", queue, err)
	}
	if _, err := s.RetryCaseAction(ctx, quack.RetryCaseActionParams{GuildID: guildID, ExecutionID: succeeded.ID}); err == nil {
		t.Error("retried a succeeded execution")
	}

	retry := quack.RetryCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	first, err := s.RetryCaseAction(ctx, retry)
	if err != nil || first.Status != quack.ActionExecutionPending || first.LastErrorCode != "" {
		t.Fatalf("retry = %+v, %v", first, err)
	}
	if second, err := s.RetryCaseAction(ctx, retry); err != nil || second.ID != first.ID || second.Status != quack.ActionExecutionPending {
		t.Fatalf("repeat retry = %+v, %v", second, err)
	}
	if err := notFound(s.RetryCaseAction(ctx, quack.RetryCaseActionParams{GuildID: "other", ExecutionID: failed.ID})); err != nil {
		t.Errorf("retry across guilds: %v", err)
	}

	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionFailed)
	dismiss := quack.DismissCaseActionParams{GuildID: guildID, ExecutionID: failed.ID, ActorDiscordUserID: "mod"}
	dismissed, err := s.DismissCaseAction(ctx, dismiss)
	if err != nil || dismissed.DismissedAt == nil || dismissed.DismissedByDiscordUserID != "mod" {
		t.Fatalf("dismiss = %+v, %v", dismissed, err)
	}
	if again, err := s.DismissCaseAction(ctx, dismiss); err != nil || !again.DismissedAt.Equal(*dismissed.DismissedAt) {
		t.Fatalf("repeat dismiss = %+v, %v", again, err)
	}
	if queue, err := s.ListFailedCaseActions(ctx, quack.FailedCaseActionFilter{GuildID: guildID}); err != nil || queue.Total != 0 {
		t.Fatalf("review queue after dismissal = %+v, %v", queue, err)
	}

	reversal := quack.QueueCaseReversalParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "mod",
		OriginalExecutionID: succeeded.ID, ActionType: quack.ActionRemoveTimeout}
	firstReversal, err := s.QueueCaseReversal(ctx, reversal)
	if err != nil || firstReversal.SafeForRetry || firstReversal.Position != 2 || *firstReversal.ReversalOfExecutionID != succeeded.ID {
		t.Fatalf("reversal = %+v, %v", firstReversal, err)
	}
	if second, err := s.QueueCaseReversal(ctx, reversal); err != nil || second.ID != firstReversal.ID {
		t.Fatalf("repeat reversal = %+v, %v", second, err)
	}
	reversal.CaseID = "other-case"
	if err := notFound(s.QueueCaseReversal(ctx, reversal)); err != nil {
		t.Errorf("reversal of another case's execution: %v", err)
	}

	events, err := s.ListCaseEvents(ctx, created.Case.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[quack.CaseEventType]int{}
	for _, event := range events {
		counts[event.EventType]++
	}
	if counts[quack.CaseEventActionRetried] != 1 || counts[quack.CaseEventActionDismissed] != 1 || counts[quack.CaseEventReversalQueued] != 1 {
		t.Fatalf("review events = %v; want one of each, even after repeats", counts)
	}
}
