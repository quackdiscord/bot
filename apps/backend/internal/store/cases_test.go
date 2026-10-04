package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestCreateCaseWritesEverything(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	template := createTemplate(t, s, guildID, "spam")
	actionID := template.Levels[1].Actions[0].ID
	created, err := s.CreateCase(ctx, quack.CreateCaseParams{
		Case:  newCase(guildID, &template.Template.ID),
		Event: newCaseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{TemplateActionID: &actionID, Position: 1, ActionType: quack.ActionTimeoutUser, SafeForRetry: true},
			{Position: 2, ActionType: quack.ActionKickUser},
		},
		Evidence: []quack.CaseEvidenceSnapshot{{
			ULIDModel: quack.ULIDModel{ID: "01KEVIDENCE000000000000001"}, ChannelDiscordID: "c", MessageDiscordID: "m",
			AuthorDiscordUserID: "target-1", MessageURL: "https://discord.com/channels/g/c/m", Content: "spam", CaptureOutcome: "captured",
		}},
		Attachments:  []quack.CaseEvidenceAttachment{{EvidenceID: "01KEVIDENCE000000000000001", Filename: "a.png", CopyOutcome: "copied"}},
		Notification: pendingNotification(),
		Audit: &quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "case.create",
			ResourceType: "case", Result: quack.AuditResultSuccess},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Case.CaseNumber != 1 || created.Case.ContextValuesJSON != "[]" || created.Notification.Status != quack.NotificationPending {
		t.Fatalf("created = %+v", created)
	}
	executions, err := s.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil || len(executions) != 2 || executions[0].Status != quack.ActionExecutionPending ||
		executions[0].IdempotencyKey == "" || !executions[0].SafeForRetry || executions[1].SafeForRetry {
		t.Fatalf("executions = %+v, %v", executions, err)
	}
	events, err := s.ListCaseEvents(ctx, created.Case.ID)
	if err != nil || len(events) != 1 || events[0].EventType != quack.CaseEventCreated || events[0].GuildID != guildID {
		t.Fatalf("events = %+v, %v", events, err)
	}
	evidence, attachments, err := s.ListCaseEvidence(ctx, created.Case.ID)
	if err != nil || len(evidence) != 1 || evidence[0].EmbedsJSON != "[]" || len(attachments) != 1 {
		t.Fatalf("evidence = %+v, %+v, %v", evidence, attachments, err)
	}
	audits, err := s.ListAuditLogEntries(ctx, guildID)
	if err != nil || len(audits) != 1 || audits[0].ResourceID != created.Case.ID {
		t.Fatalf("audits = %+v, %v", audits, err)
	}
}

func TestCreateCaseNumbersPerGuild(t *testing.T) {
	s, guildID := newTestStore(t)
	otherID := addGuild(t, s, "guild-2")
	first := createCase(t, s, guildID, nil, nil)
	second := createCase(t, s, guildID, nil, nil)
	other := createCase(t, s, otherID, nil, nil)
	if first.Case.CaseNumber != 1 || second.Case.CaseNumber != 2 || other.Case.CaseNumber != 1 {
		t.Fatalf("case numbers = %d, %d, %d; want 1, 2, 1", first.Case.CaseNumber, second.Case.CaseNumber, other.Case.CaseNumber)
	}
}

func TestCreateCaseRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	_, err := s.CreateCase(ctx, quack.CreateCaseParams{
		Case:  newCase(guildID, nil),
		Event: newCaseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionTimeoutUser, IdempotencyKey: "duplicate"},
			{Position: 2, ActionType: quack.ActionKickUser, IdempotencyKey: "duplicate"},
		},
		Audit: &quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "case.create",
			ResourceType: "case", Result: quack.AuditResultSuccess},
	})
	if err == nil {
		t.Fatal("CreateCase with duplicate execution keys succeeded")
	}
	cases, _ := s.ListCases(ctx, guildID)
	audits, _ := s.ListAuditLogEntries(ctx, guildID)
	if len(cases) != 0 || len(audits) != 0 {
		t.Fatalf("failed create left %d cases and %d audits", len(cases), len(audits))
	}
}

func TestVoidCase(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	executions := append(timeout(0), quack.CaseActionExecution{Position: 1, ActionType: quack.ActionKickUser})
	created := createCase(t, s, guildID, executions, pendingNotification())
	running := claim(t, s, created.Case.ID)

	void := quack.VoidCaseParams{GuildID: guildID, CaseID: created.Case.ID, ActorDiscordUserID: "moderator-2", Reason: "Wrong member"}
	voided, err := s.VoidCase(ctx, void)
	if err != nil || voided.Validity != quack.CaseValidityVoided || voided.VoidedReason != "Wrong member" || voided.VoidedAt == nil {
		t.Fatalf("VoidCase = %+v, %v", voided, err)
	}
	got, err := s.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ID != running.Execution.ID || got[0].Status != quack.ActionExecutionRunning {
		t.Errorf("void touched a running execution: %+v", got[0])
	}
	if got[1].Status != quack.ActionExecutionCancelled || got[1].LastErrorCode != "case_voided" {
		t.Errorf("void left a pending execution: %+v", got[1])
	}
	if n, err := s.GetCaseNotification(ctx, created.Case.ID); err != nil || n.Status != quack.NotificationFailed {
		t.Errorf("notification after void = %+v, %v", n, err)
	}

	if again, err := s.VoidCase(ctx, void); err != nil || again.ID != created.Case.ID {
		t.Errorf("repeat void = %+v, %v; want a no-op", again, err)
	}
	void.Reason = "Different"
	if _, err := s.VoidCase(ctx, void); err == nil {
		t.Error("void with a different reason succeeded")
	}
	if err := notFound(s.VoidCase(ctx, quack.VoidCaseParams{GuildID: "other", CaseID: created.Case.ID, Reason: "x"})); err != nil {
		t.Errorf("void across guilds: %v", err)
	}
}

func TestReplacementCaseLinksOnce(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	original := createCase(t, s, guildID, nil, nil)
	if _, err := s.VoidCase(ctx, quack.VoidCaseParams{GuildID: guildID, CaseID: original.Case.ID, Reason: "Wrong level"}); err != nil {
		t.Fatal(err)
	}
	replacement := newCase(guildID, nil)
	replacement.ReplacesCaseID = &original.Case.ID
	created, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: replacement, Event: newCaseEvent()})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.GetCaseByID(ctx, original.Case.ID)
	if err != nil || linked.ReplacementCaseID == nil || *linked.ReplacementCaseID != created.Case.ID {
		t.Fatalf("original = %+v, %v", linked, err)
	}
	if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: replacement, Event: newCaseEvent()}); err == nil {
		t.Fatal("second replacement of one case succeeded")
	}
}
