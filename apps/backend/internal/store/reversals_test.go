package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func voidCase(t *testing.T, s *store.Store, guildID, caseID string) {
	t.Helper()
	if _, err := s.VoidCase(context.Background(), quack.VoidCaseParams{
		GuildID: guildID, CaseID: caseID, ActorDiscordUserID: "moderator-2", Reason: "mistake",
	}); err != nil {
		t.Fatalf("void case: %v", err)
	}
}

func TestVoidReversesOnlySucceededPunishments(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, append(timeout(0), quack.CaseActionExecution{
		Position: 1, ActionType: quack.ActionKickUser, ConfigSnapshotJSON: "{}", SafeForRetry: true,
	}), nil)
	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionSucceeded)
	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionSucceeded)

	voidCase(t, s, guildID, created.Case.ID)
	voidCase(t, s, guildID, created.Case.ID) // repeating the void queues nothing more
	executions, err := s.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil || len(executions) != 3 {
		t.Fatalf("executions = %+v, %v", executions, err)
	}
	reversal := executions[2]
	if reversal.ActionType != quack.ActionRemoveTimeout || reversal.ReversalOfExecutionID == nil ||
		*reversal.ReversalOfExecutionID != executions[0].ID || reversal.ReversalAppealID != nil ||
		reversal.ConfigSnapshotJSON != `{"requested_by":"moderator-2"}` || reversal.SafeForRetry {
		t.Fatalf("reversal = %+v", reversal)
	}
	audits, err := s.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: string(quack.AuditActionActionReverse), Limit: 10})
	if err != nil || audits.Total != 1 || audits.Entries[0].ActorDiscordUserID != "moderator-2" || audits.Entries[0].Source != quack.AuditSourceSystem {
		t.Fatalf("reversal audit = %+v, %v", audits, err)
	}
}

func TestPunishmentFinishingAfterVoidIsReversedNotRetried(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)

	// A punishment that succeeds after its case was voided is undone.
	late := createCase(t, s, guildID, timeout(0), nil)
	running := claim(t, s, late.Case.ID)
	voidCase(t, s, guildID, late.Case.ID)
	if err := s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID: running.Execution.ID, LeaseToken: running.Execution.LeaseToken, AttemptNumber: 1,
		AttemptStatus: quack.ActionAttemptSucceeded, ExecutionStatus: quack.ActionExecutionSucceeded,
		ResponsePayloadJSON: `{"timeout_until":"2030-01-01T00:00:00Z"}`,
	}); err != nil {
		t.Fatal(err)
	}
	executions, _ := s.ListCaseActionExecutions(ctx, late.Case.ID)
	if len(executions) != 2 || executions[1].ActionType != quack.ActionRemoveTimeout {
		t.Fatalf("late success was not reversed: %+v", executions)
	}

	// A punishment that would retry after its case was voided goes to review.
	retrying := createCase(t, s, guildID, timeout(0), nil)
	running = claim(t, s, retrying.Case.ID)
	voidCase(t, s, guildID, retrying.Case.ID)
	next := time.Now().Add(time.Minute)
	if err := s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID: running.Execution.ID, LeaseToken: running.Execution.LeaseToken, AttemptNumber: 1,
		AttemptStatus: quack.ActionAttemptFailed, ExecutionStatus: quack.ActionExecutionRetrying, NextRetryAt: &next,
		ErrorCode: "rate_limited", ErrorMessage: "slow down",
	}); err != nil {
		t.Fatal(err)
	}
	executions, _ = s.ListCaseActionExecutions(ctx, retrying.Case.ID)
	if len(executions) != 1 || executions[0].Status != quack.ActionExecutionFailed || executions[0].NextRetryAt != nil {
		t.Fatalf("voided retry = %+v", executions)
	}
}

func TestReversalNoopIsAudited(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	complete(t, s, claim(t, s, created.Case.ID), quack.ActionExecutionSucceeded)
	voidCase(t, s, guildID, created.Case.ID)
	reversal := claim(t, s, created.Case.ID)
	if err := s.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID: reversal.Execution.ID, LeaseToken: reversal.Execution.LeaseToken, AttemptNumber: 1,
		AttemptStatus: quack.ActionAttemptSucceeded, ExecutionStatus: quack.ActionExecutionSucceeded,
		ResponsePayloadJSON: `{"reversal_noop":true}`,
	}); err != nil {
		t.Fatal(err)
	}
	audits, err := s.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: string(quack.AuditActionActionSucceeded), ResourceID: reversal.Execution.ID, Limit: 10})
	if err != nil || audits.Total != 1 || !strings.Contains(audits.Entries[0].MetadataJSON, `"reversal_noop":true`) {
		t.Fatalf("reversal audit = %+v, %v", audits, err)
	}
}

func TestCompetingPunishmentExists(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	first := createCase(t, s, guildID, timeout(0), nil)
	complete(t, s, claim(t, s, first.Case.ID), quack.ActionExecutionSucceeded)
	original := first.ActionExecutions[0].ID
	if competing, err := s.CompetingPunishmentExists(ctx, guildID, first.Case.ID, original); err != nil || competing {
		t.Fatalf("alone = %v, %v", competing, err)
	}

	// Another member's timeout does not compete.
	other := newCase(guildID, nil)
	other.TargetDiscordUserID = "target-2"
	if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: other, Event: newCaseEvent(), ActionExecutions: timeout(0)}); err != nil {
		t.Fatal(err)
	}
	if competing, err := s.CompetingPunishmentExists(ctx, guildID, first.Case.ID, original); err != nil || competing {
		t.Fatalf("other member = %v, %v", competing, err)
	}
	// A queued timeout for the same member does.
	createCase(t, s, guildID, timeout(0), nil)
	if competing, err := s.CompetingPunishmentExists(ctx, guildID, first.Case.ID, original); err != nil || !competing {
		t.Fatalf("same member = %v, %v", competing, err)
	}
}

func TestTemplateCaseCountHonorsDecayCutoff(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	template := createTemplate(t, s, guildID, "spam")
	for range 2 {
		c := newCase(guildID, &template.Template.ID)
		if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()}); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().UTC().AddDate(0, 0, -10)
	if err := s.DB().Table("cases").Where("case_number = 1").Update("created_at", old).Error; err != nil {
		t.Fatal(err)
	}
	params := quack.CountTemplateCasesForTargetParams{GuildID: guildID, TemplateID: template.Template.ID, TargetDiscordUserID: "target-1"}
	if count, err := s.CountTemplateCasesForTarget(ctx, params); err != nil || count != 2 {
		t.Fatalf("all-time count = %d, %v", count, err)
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -5)
	params.CreatedAtOrAfter = &cutoff
	if count, err := s.CountTemplateCasesForTarget(ctx, params); err != nil || count != 1 {
		t.Fatalf("windowed count = %d, %v", count, err)
	}
}
