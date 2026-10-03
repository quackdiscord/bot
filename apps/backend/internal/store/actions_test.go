package store_test

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestClaimAndCompleteInOrder(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	executions := append(timeout(1), quack.CaseActionExecution{Position: 2, ActionType: quack.ActionKickUser})
	created := createCase(t, s, guildID, executions, nil)

	claimed := claim(t, s, created.Case.ID)
	if claimed == nil || claimed.Execution.Position != 1 || claimed.Execution.AttemptCount != 1 ||
		claimed.Execution.Status != quack.ActionExecutionRunning || claimed.Execution.LeaseToken == "" || claimed.Case.ID != created.Case.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	if again := claim(t, s, created.Case.ID); again != nil {
		t.Fatalf("claimed a second execution while one is leased: %+v", again)
	}
	if err := s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID: claimed.Execution.ID, LeaseToken: claimed.Execution.LeaseToken, AttemptNumber: 1, WorkerID: "worker",
		AttemptStatus: quack.ActionAttemptFailed, ExecutionStatus: quack.ActionExecutionFailed,
		ErrorCode: "missing_permissions", ErrorMessage: "Quack cannot time out this member",
		EventType: quack.CaseEventActionFailed, EventBody: "Timeout failed",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil || got[0].Status != quack.ActionExecutionFailed || got[0].LeaseToken != "" || got[1].Status != quack.ActionExecutionPending {
		t.Fatalf("executions = %+v, %v", got, err)
	}
	attempts, err := s.ListCaseActionAttempts(ctx, []string{claimed.Execution.ID})
	if err != nil || len(attempts) != 1 || attempts[0].Status != quack.ActionAttemptFailed || attempts[0].ErrorCode != "missing_permissions" {
		t.Fatalf("attempts = %+v, %v", attempts, err)
	}
	audits, err := s.ListAuditLogEntries(ctx, guildID)
	if err != nil || len(audits) != 2 || audits[0].Action != string(quack.AuditActionActionAttempt) || audits[1].Action != "case_action.failed" {
		t.Fatalf("audits = %+v, %v", audits, err)
	}
	if next := claim(t, s, created.Case.ID); next == nil || next.Execution.Position != 2 {
		t.Fatalf("next claim = %+v, want position 2", next)
	}
}

func TestClaimHasOneWinner(t *testing.T) {
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			claimed, err := s.ClaimNextCaseAction(context.Background(), quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker"})
			if err == nil && claimed != nil {
				winners.Add(1)
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("%d claim winners, want 1", winners.Load())
	}
}

// TestCompleteRequiresLiveLease checks that only the worker holding the
// lease on a running execution can record its outcome.
func TestCompleteRequiresLiveLease(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	claimed := claim(t, s, created.Case.ID)
	params := func(token string) quack.CompleteCaseActionParams {
		return quack.CompleteCaseActionParams{ExecutionID: claimed.Execution.ID, LeaseToken: token, WorkerID: "worker",
			AttemptStatus: quack.ActionAttemptSucceeded, ExecutionStatus: quack.ActionExecutionSucceeded}
	}
	for name, token := range map[string]string{"empty token": "", "wrong token": "not-the-lease"} {
		if err := s.CompleteCaseAction(ctx, params(token)); err == nil {
			t.Errorf("%s: completion succeeded", name)
		}
	}
	if err := s.CompleteCaseAction(ctx, params(claimed.Execution.LeaseToken)); err != nil {
		t.Fatalf("lease holder: %v", err)
	}
	if err := s.CompleteCaseAction(ctx, params(claimed.Execution.LeaseToken)); err == nil {
		t.Error("completed an execution that is no longer running")
	}
}

// TestExpiredLeaseGoesToReview checks that an abandoned execution is never
// retried automatically, whatever its retry settings, and that the old
// worker can no longer complete it.
func TestExpiredLeaseGoesToReview(t *testing.T) {
	for _, execution := range []quack.CaseActionExecution{
		{ActionType: quack.ActionBanUser, SafeForRetry: true, MaxRetries: 3},
		{ActionType: quack.ActionKickUser, MaxRetries: 3},
		{ActionType: quack.ActionTimeoutUser, SafeForRetry: true},
	} {
		t.Run(string(execution.ActionType), func(t *testing.T) {
			ctx := context.Background()
			s, guildID := newTestStore(t)
			created := createCase(t, s, guildID, []quack.CaseActionExecution{execution}, nil)
			first := claim(t, s, created.Case.ID)
			expire(t, s, "case_action_executions", first.Execution.ID)
			if second := claim(t, s, created.Case.ID); second != nil {
				t.Fatalf("expired execution was claimed again: %+v", second)
			}
			current, err := s.GetCaseActionExecution(ctx, guildID, first.Execution.ID)
			if err != nil || current.Status != quack.ActionExecutionFailed || current.AttemptCount != 1 ||
				current.LeaseToken != "" || current.LastErrorCode != "lease_expired_review_required" {
				t.Fatalf("execution = %+v, %v", current, err)
			}
			attempts, err := s.ListCaseActionAttempts(ctx, []string{first.Execution.ID})
			if err != nil || len(attempts) != 1 || attempts[0].Status != quack.ActionAttemptFailed || attempts[0].ErrorCode != "lease_expired" {
				t.Fatalf("attempts = %+v, %v", attempts, err)
			}
			err = s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{ExecutionID: first.Execution.ID, LeaseToken: first.Execution.LeaseToken,
				WorkerID: "old", AttemptStatus: quack.ActionAttemptSucceeded, ExecutionStatus: quack.ActionExecutionSucceeded})
			if err == nil {
				t.Fatal("expired worker overwrote the review decision")
			}
		})
	}
}

func TestRetryingExecutionWaitsForItsTime(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	claimed := claim(t, s, created.Case.ID)
	later := time.Now().UTC().Add(time.Hour)
	if err := s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID: claimed.Execution.ID, LeaseToken: claimed.Execution.LeaseToken, WorkerID: "worker",
		AttemptStatus: quack.ActionAttemptFailed, ExecutionStatus: quack.ActionExecutionRetrying,
		ErrorCode: "rate_limited", NextRetryAt: &later,
	}); err != nil {
		t.Fatal(err)
	}
	if ids := poll(t, s, 10); len(ids) != 0 {
		t.Fatalf("polled a retry before its time: %v", ids)
	}
	if again := claim(t, s, created.Case.ID); again != nil {
		t.Fatalf("claimed a retry before its time: %+v", again)
	}
	if err := s.DB().Table("case_action_executions").Where("id = ?", claimed.Execution.ID).
		Update("next_retry_at", time.Now().UTC().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if ids := poll(t, s, 10); !slices.Equal(ids, []string{created.Case.ID}) {
		t.Fatalf("poll after retry time = %v", ids)
	}
	if retried := claim(t, s, created.Case.ID); retried == nil || retried.Execution.AttemptCount != 2 {
		t.Fatalf("retry claim = %+v", retried)
	}
}

func TestPollIsFairAcrossGuilds(t *testing.T) {
	s, guildOne := newTestStore(t)
	guildTwo := addGuild(t, s, "guild-2")
	guildThree := addGuild(t, s, "guild-3")
	guildOf := map[string]string{}
	base := time.Now().UTC().Add(-time.Hour)
	for i := range 5 {
		guildOf[dueCase(t, s, guildOne, 1, base.Add(time.Duration(i)*time.Minute))] = guildOne
	}
	guildOf[dueCase(t, s, guildTwo, 1, base.Add(10*time.Minute))] = guildTwo
	guildOf[dueCase(t, s, guildThree, 1, base.Add(20*time.Minute))] = guildThree

	seen := map[string]bool{}
	for round := range 2 {
		ids := poll(t, s, 2)
		if len(ids) != 2 || guildOf[ids[0]] == guildOf[ids[1]] {
			t.Fatalf("round %d = %v; want one case from each of two guilds", round, ids)
		}
		seen[guildOf[ids[0]]], seen[guildOf[ids[1]]] = true, true
	}
	if len(seen) != 3 {
		t.Fatalf("two rounds of two served %d guilds; the cursor should rotate through all three", len(seen))
	}
}

func TestPollPrefersEarlierPositionsWithinGuild(t *testing.T) {
	s, guildID := newTestStore(t)
	later := dueCase(t, s, guildID, 2, time.Now().UTC().Add(-time.Hour))
	first := dueCase(t, s, guildID, 1, time.Now().UTC())
	if ids := poll(t, s, 2); !slices.Equal(ids, []string{first, later}) {
		t.Fatalf("poll = %v, want %v", ids, []string{first, later})
	}
}

// TestPollFindsNotifyOnlyCases covers cases whose level notifies without
// enforcing: they have a notification and no executions, so the poller must
// find them through case_notifications or the DM is never sent.
func TestPollFindsNotifyOnlyCases(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	notifyOnly := createCase(t, s, guildID, nil, pendingNotification())
	if ids := poll(t, s, 10); !slices.Equal(ids, []string{notifyOnly.Case.ID}) {
		t.Fatalf("poll = %v, want the notify-only case", ids)
	}

	claimed, err := s.ClaimCaseNotification(ctx, quack.ClaimCaseNotificationParams{CaseID: notifyOnly.Case.ID, WorkerID: "worker"})
	if err != nil || claimed == nil {
		t.Fatalf("claim notification = %+v, %v", claimed, err)
	}
	if ids := poll(t, s, 10); len(ids) != 0 {
		t.Fatalf("polled a notification under a live lease: %v", ids)
	}
	expire(t, s, "case_notifications", claimed.ID)
	if ids := poll(t, s, 10); !slices.Equal(ids, []string{notifyOnly.Case.ID}) {
		t.Fatalf("poll after lease expiry = %v, want the notify-only case", ids)
	}
}

func TestPollHoldsNotificationsUntilEnforcementSettles(t *testing.T) {
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), pendingNotification())
	claimed := claim(t, s, created.Case.ID)
	if ids := poll(t, s, 10); len(ids) != 0 {
		t.Fatalf("polled a case whose enforcement is running: %v", ids)
	}
	complete(t, s, claimed, quack.ActionExecutionSucceeded)
	if ids := poll(t, s, 10); !slices.Equal(ids, []string{created.Case.ID}) {
		t.Fatalf("poll after enforcement = %v, want the case for its notification", ids)
	}
}

func poll(t *testing.T, s *store.Store, limit int) []string {
	t.Helper()
	ids, err := s.ListExecutableCaseIDs(context.Background(), limit)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	return ids
}

// dueCase creates a case with one due execution at position, created at
// readyAt.
func dueCase(t *testing.T, s *store.Store, guildID string, position int, readyAt time.Time) string {
	t.Helper()
	created := createCase(t, s, guildID, timeout(position), nil)
	if err := s.DB().Table("case_action_executions").Where("case_id = ?", created.Case.ID).Update("created_at", readyAt).Error; err != nil {
		t.Fatal(err)
	}
	return created.Case.ID
}
