package storage_test

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestCreateCasePersistsCaseEventActionsAndAudit(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	template := createCaseStorageTemplate(t, store, guildID)

	created, err := store.CreateCase(ctx, storage.CreateCaseParams{
		Case: caseModel(guildID, &template.Template.ID),
		Event: structs.CaseEvent{
			EventType:          structs.CaseEventCreated,
			ActorDiscordUserID: "moderator-1",
			Body:               "Case created",
		},
		ActionExecutions: []structs.CaseActionExecution{
			{TemplateActionID: &template.Actions[0].ID, Position: 1, ActionType: structs.ActionRecordWarning, ConfigSnapshotJSON: `{}`},
			{TemplateActionID: &template.Actions[1].ID, Position: 2, ActionType: structs.ActionWriteModLog, ConfigSnapshotJSON: `{}`},
		},
		Audit: &structs.AuditLogEntry{
			GuildID:            guildID,
			ActorDiscordUserID: "moderator-1",
			Source:             structs.AuditSourceAPI,
			Action:             "case.create",
			ResourceType:       "case",
			Result:             structs.AuditResultSuccess,
			MetadataJSON:       "{}",
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if created.Case.ID == "" || created.Case.CaseNumber != 1 {
		t.Fatalf("unexpected case model: %+v", created.Case)
	}
	if len(created.ActionExecutions) != 2 {
		t.Fatalf("expected action executions")
	}

	events, err := store.ListCaseEvents(ctx, created.Case.ID)
	if err != nil {
		t.Fatalf("list case events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != structs.CaseEventCreated {
		t.Fatalf("expected created event, got %+v", events)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatalf("list case action executions: %v", err)
	}
	if len(actions) != 2 || actions[0].Position != 1 || actions[1].Position != 2 {
		t.Fatalf("expected ordered actions, got %+v", actions)
	}
	if actions[0].Status != structs.ActionExecutionPending {
		t.Fatalf("expected pending action, got %s", actions[0].Status)
	}

	audits, err := store.ListAuditLogEntries(ctx, guildID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 1 || audits[0].ResourceID != created.Case.ID {
		t.Fatalf("expected case audit, got %+v", audits)
	}
}

func TestCreateCaseAllocatesCaseNumbersPerGuild(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	guildTwo, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "guild-2",
		Name:               "Guild Two",
		OwnerDiscordUserID: "owner-2",
	})
	if err != nil {
		t.Fatalf("upsert second guild: %v", err)
	}

	first, err := store.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create first case: %v", err)
	}
	second, err := store.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}
	otherGuild, err := store.CreateCase(ctx, storage.CreateCaseParams{Case: caseModel(guildTwo.ID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create other guild case: %v", err)
	}

	if first.Case.CaseNumber != 1 || second.Case.CaseNumber != 2 || otherGuild.Case.CaseNumber != 1 {
		t.Fatalf("unexpected case numbers: first=%d second=%d other=%d", first.Case.CaseNumber, second.Case.CaseNumber, otherGuild.Case.CaseNumber)
	}
}

func TestCreateCaseRollsBackOnActionFailure(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	_, err := store.CreateCase(ctx, storage.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []structs.CaseActionExecution{
			{Position: 1, ActionType: structs.ActionRecordWarning, IdempotencyKey: "duplicate-key", ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: structs.ActionWriteModLog, IdempotencyKey: "duplicate-key", ConfigSnapshotJSON: `{}`},
		},
		Audit: &structs.AuditLogEntry{
			GuildID:      guildID,
			Source:       structs.AuditSourceAPI,
			Action:       "case.create",
			ResourceType: "case",
			Result:       structs.AuditResultSuccess,
			MetadataJSON: "{}",
		},
	})
	if err == nil {
		t.Fatalf("expected action insert failure")
	}

	cases, err := store.ListCases(ctx, guildID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if len(cases) != 0 {
		t.Fatalf("expected rollback to remove case, got %+v", cases)
	}
	audits, err := store.ListAuditLogEntries(ctx, guildID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 0 {
		t.Fatalf("expected rollback to remove audit, got %+v", audits)
	}
}

func TestCaseActionStateMachineClaimCompleteAndSkip(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCase(ctx, storage.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []structs.CaseActionExecution{
			{Position: 1, ActionType: structs.ActionSendDM, ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: structs.ActionWriteModLog, ConfigSnapshotJSON: `{}`},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	claimed, err := store.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil {
		t.Fatalf("claim action: %v", err)
	}
	if claimed == nil || claimed.Execution.Position != 1 || claimed.Execution.AttemptCount != 1 || claimed.Execution.Status != structs.ActionExecutionRunning {
		t.Fatalf("unexpected claimed action: %+v", claimed)
	}

	if err := store.CompleteCaseAction(ctx, storage.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            "worker-1",
		AttemptStatus:       structs.ActionAttemptFailed,
		ExecutionStatus:     structs.ActionExecutionFailed,
		ErrorCode:           "dm_closed",
		ErrorMessage:        "user cannot receive DMs",
		RequestPayloadJSON:  `{}`,
		ResponsePayloadJSON: `{}`,
		EventType:           structs.CaseEventActionFailed,
		EventBody:           "DM failed",
		EventMetadataJSON:   `{}`,
	}); err != nil {
		t.Fatalf("complete failed action: %v", err)
	}
	if err := store.SkipCaseActions(ctx, storage.SkipCaseActionsParams{
		CaseID:        created.Case.ID,
		AfterPosition: claimed.Execution.Position,
		Reason:        "previous action failed",
	}); err != nil {
		t.Fatalf("skip remaining actions: %v", err)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != structs.ActionExecutionFailed || actions[1].Status != structs.ActionExecutionSkipped {
		t.Fatalf("expected failed then skipped actions, got %+v", actions)
	}

	var attempts []structs.CaseActionAttempt
	if err := store.DB().Where("execution_id = ?", claimed.Execution.ID).Find(&attempts).Error; err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Status != structs.ActionAttemptFailed || attempts[0].AttemptNumber != 1 {
		t.Fatalf("unexpected attempts: %+v", attempts)
	}

	cases, err := store.ListCases(ctx, guildID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if cases[0].Status != structs.CaseStatusFailed || cases[0].ResolvedAt == nil {
		t.Fatalf("expected failed resolved case, got %+v", cases[0])
	}
}

func TestCaseActionRetryScheduling(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCase(ctx, storage.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []structs.CaseActionExecution{
			{Position: 1, ActionType: structs.ActionSendDM, ConfigSnapshotJSON: `{}`, MaxRetries: 1, RetryBackoffMS: 100},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	claimed, err := store.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil {
		t.Fatalf("claim action: %v", err)
	}
	nextRetryAt := time.Now().UTC().Add(time.Hour)
	if err := store.CompleteCaseAction(ctx, storage.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            "worker-1",
		AttemptStatus:       structs.ActionAttemptFailed,
		ExecutionStatus:     structs.ActionExecutionRetrying,
		ErrorCode:           "rate_limited",
		ErrorMessage:        "rate limited",
		RequestPayloadJSON:  `{}`,
		ResponsePayloadJSON: `{}`,
		NextRetryAt:         &nextRetryAt,
		EventType:           structs.CaseEventActionFailed,
		EventBody:           "will retry",
		EventMetadataJSON:   `{}`,
	}); err != nil {
		t.Fatalf("complete retrying action: %v", err)
	}

	caseIDs, err := store.ListExecutableCaseIDs(ctx, 10)
	if err != nil {
		t.Fatalf("list executable case ids: %v", err)
	}
	if len(caseIDs) != 0 {
		t.Fatalf("expected no executable case before retry time, got %+v", caseIDs)
	}

	past := time.Now().UTC().Add(-time.Minute)
	if err := store.DB().Model(&structs.CaseActionExecution{}).Where("id = ?", claimed.Execution.ID).Update("next_retry_at", past).Error; err != nil {
		t.Fatalf("make retry eligible: %v", err)
	}
	caseIDs, err = store.ListExecutableCaseIDs(ctx, 10)
	if err != nil {
		t.Fatalf("list executable case ids: %v", err)
	}
	if len(caseIDs) != 1 || caseIDs[0] != created.Case.ID {
		t.Fatalf("expected executable case after retry time, got %+v", caseIDs)
	}
}

func createCaseStorageTemplate(t *testing.T, store *storage.Store, guildID string) *storage.ExpandedCaseTemplate {
	t.Helper()

	created, err := store.CreateCaseTemplate(context.Background(), storage.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Actions: []structs.CaseTemplateAction{
			{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true},
			{Position: 2, ActionType: structs.ActionWriteModLog, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true},
			{Position: 3, ActionType: structs.ActionSendDM, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: false},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func caseModel(guildID string, templateID *string) structs.Case {
	return structs.Case{
		GuildID:                guildID,
		TemplateID:             templateID,
		TemplateVersion:        1,
		TemplateSnapshotJSON:   "{}",
		TargetDiscordUserID:    "target-1",
		ModeratorDiscordUserID: "moderator-1",
		Reason:                 "No spam",
		Severity:               structs.CaseSeverityMedium,
		Weight:                 1,
		Status:                 structs.CaseStatusOpen,
		Source:                 structs.CaseSourceAPI,
		MetadataJSON:           "{}",
	}
}

func caseEvent() structs.CaseEvent {
	return structs.CaseEvent{
		EventType:          structs.CaseEventCreated,
		ActorDiscordUserID: "moderator-1",
		Body:               "Case created",
		MetadataJSON:       "{}",
	}
}
