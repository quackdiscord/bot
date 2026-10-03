package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestCreateCasePersistsCaseEventActionsAndAudit(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	template := createCaseStorageTemplate(t, store, guildID)

	created, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case: caseModel(guildID, &template.Template.ID),
		Event: quack.CaseEvent{
			EventType:          quack.CaseEventCreated,
			ActorDiscordUserID: "moderator-1",
			Body:               "Case created",
		},
		ActionExecutions: []quack.CaseActionExecution{
			{TemplateActionID: &template.Levels[0].Actions[0].ID, Position: 1, ActionType: quack.ActionTimeoutUser, ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: quack.ActionKickUser, ConfigSnapshotJSON: `{}`},
		},
		Audit: &quack.AuditLogEntry{
			GuildID:            guildID,
			ActorDiscordUserID: "moderator-1",
			Source:             quack.AuditSourceAPI,
			Action:             "case.create",
			ResourceType:       "case",
			Result:             quack.AuditResultSuccess,
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
	if len(events) != 1 || events[0].EventType != quack.CaseEventCreated {
		t.Fatalf("expected created event, got %+v", events)
	}

	actions, err := store.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatalf("list case action executions: %v", err)
	}
	if len(actions) != 2 || actions[0].Position != 1 || actions[1].Position != 2 {
		t.Fatalf("expected ordered actions, got %+v", actions)
	}
	if actions[0].Status != quack.ActionExecutionPending {
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
	guildTwo, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "guild-2",
		Name:               "Guild Two",
		OwnerDiscordUserID: "owner-2",
	})
	if err != nil {
		t.Fatalf("upsert second guild: %v", err)
	}

	first, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create first case: %v", err)
	}
	second, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}
	otherGuild, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildTwo.ID, nil), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create other guild case: %v", err)
	}

	if first.Case.CaseNumber != 1 || second.Case.CaseNumber != 2 || otherGuild.Case.CaseNumber != 1 {
		t.Fatalf("unexpected case numbers: first=%d second=%d other=%d", first.Case.CaseNumber, second.Case.CaseNumber, otherGuild.Case.CaseNumber)
	}
}

func TestCountTemplateCasesForTargetFiltersHistory(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	template := createCaseStorageTemplate(t, store, guildID)
	otherTemplate, err := store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "other"),
		Levels:   templateLevels(),
	})
	if err != nil {
		t.Fatalf("create other template: %v", err)
	}

	matching, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, &template.Template.ID), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create matching case: %v", err)
	}
	oldMatching, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, &template.Template.ID), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create old matching case: %v", err)
	}
	oldTime := time.Now().UTC().Add(-2 * time.Hour)
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", oldMatching.Case.ID).Update("created_at", oldTime).Error; err != nil {
		t.Fatalf("age matching case: %v", err)
	}

	voided, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, &template.Template.ID), Event: caseEvent()})
	if err != nil {
		t.Fatalf("create voided case: %v", err)
	}
	if err := store.DB().Model(&quack.Case{}).Where("id = ?", voided.Case.ID).Update("status", quack.CaseValidityVoided).Error; err != nil {
		t.Fatalf("void case: %v", err)
	}

	otherTarget := caseModel(guildID, &template.Template.ID)
	otherTarget.TargetDiscordUserID = "target-2"
	if _, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: otherTarget, Event: caseEvent()}); err != nil {
		t.Fatalf("create other target case: %v", err)
	}
	if _, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(guildID, &otherTemplate.Template.ID), Event: caseEvent()}); err != nil {
		t.Fatalf("create other template case: %v", err)
	}

	count, err := store.CountTemplateCasesForTarget(ctx, quack.CountTemplateCasesForTargetParams{
		GuildID:             guildID,
		TemplateID:          template.Template.ID,
		TargetDiscordUserID: "target-1",
	})
	if err != nil {
		t.Fatalf("count cases: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected two non-voided matching cases, got %d; first=%s", count, matching.Case.ID)
	}

}

func TestListCasesFilteredAndGetCaseByReference(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	template := createCaseStorageTemplate(t, store, guildID)
	otherGuild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "guild-2",
		Name:               "Guild Two",
		OwnerDiscordUserID: "owner-2",
	})
	if err != nil {
		t.Fatalf("upsert other guild: %v", err)
	}

	firstCase := caseModel(guildID, &template.Template.ID)
	first, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: firstCase, Event: caseEvent()})
	if err != nil {
		t.Fatalf("create first case: %v", err)
	}
	secondCase := caseModel(guildID, &template.Template.ID)
	secondCase.TargetDiscordUserID = "target-2"
	secondCase.ModeratorDiscordUserID = "moderator-2"
	secondCase.Validity = quack.CaseValidityVoided
	second, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: secondCase, Event: caseEvent()})
	if err != nil {
		t.Fatalf("create second case: %v", err)
	}
	if _, err := store.CreateCase(ctx, quack.CreateCaseParams{Case: caseModel(otherGuild.ID, &template.Template.ID), Event: caseEvent()}); err != nil {
		t.Fatalf("create other guild case: %v", err)
	}

	list, err := store.ListCasesFiltered(ctx, quack.ListCasesParams{GuildID: guildID, Limit: 10})
	if err != nil {
		t.Fatalf("list filtered cases: %v", err)
	}
	if list.Total != 2 || len(list.Cases) != 2 || list.Cases[0].ID != second.Case.ID || list.Cases[1].ID != first.Case.ID {
		t.Fatalf("expected newest-first cases for guild only, got %+v", list)
	}

	list, err = store.ListCasesFiltered(ctx, quack.ListCasesParams{GuildID: guildID, TargetDiscordUserID: "target-2", ModeratorDiscordUserID: "moderator-2", TemplateID: template.Template.ID, Validity: quack.CaseValidityVoided, Limit: 10})
	if err != nil {
		t.Fatalf("list filtered cases with filters: %v", err)
	}
	if list.Total != 1 || len(list.Cases) != 1 || list.Cases[0].ID != second.Case.ID {
		t.Fatalf("expected filtered second case, got %+v", list)
	}

	byNumber, err := store.GetCaseByIDOrNumber(ctx, guildID, "1")
	if err != nil {
		t.Fatalf("get by case number: %v", err)
	}
	if byNumber == nil || byNumber.ID != first.Case.ID {
		t.Fatalf("expected first case by number, got %+v", byNumber)
	}
	byID, err := store.GetCaseByIDOrNumber(ctx, guildID, second.Case.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if byID == nil || byID.CaseNumber != second.Case.CaseNumber {
		t.Fatalf("expected second case by id, got %+v", byID)
	}
	crossGuild, err := store.GetCaseByIDOrNumber(ctx, otherGuild.ID, second.Case.ID)
	if err != nil {
		t.Fatalf("get cross guild: %v", err)
	}
	if crossGuild != nil {
		t.Fatalf("expected cross guild lookup to miss, got %+v", crossGuild)
	}
}

func TestListCaseActionAttemptsAndTargetSummary(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionSendDM, ConfigSnapshotJSON: `{}`},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	action := created.ActionExecutions[0]
	if _, err := store.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"}); err != nil {
		t.Fatalf("claim action: %v", err)
	}
	if err := store.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID:         action.ID,
		AttemptNumber:       1,
		WorkerID:            "worker-1",
		AttemptStatus:       quack.ActionAttemptSucceeded,
		ExecutionStatus:     quack.ActionExecutionSucceeded,
		RequestPayloadJSON:  `{}`,
		ResponsePayloadJSON: `{}`,
	}); err != nil {
		t.Fatalf("complete action: %v", err)
	}

	attempts, err := store.ListCaseActionAttempts(ctx, []string{action.ID})
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].ExecutionID != action.ID || attempts[0].AttemptNumber != 1 {
		t.Fatalf("unexpected attempts: %+v", attempts)
	}

	summary, err := store.TargetCaseSummary(ctx, guildID, "target-1")
	if err != nil {
		t.Fatalf("target summary: %v", err)
	}
	if summary.Total != 1 || summary.ByValidity[quack.CaseValidityValid] != 1 {
		t.Fatalf("unexpected target summary: %+v", summary)
	}
}

func TestListAuditLogEntriesFiltered(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	otherGuild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "guild-2",
		Name:               "Guild Two",
		OwnerDiscordUserID: "owner-2",
	})
	if err != nil {
		t.Fatalf("upsert other guild: %v", err)
	}

	entries := []quack.AuditLogEntry{
		{GuildID: guildID, ActorDiscordUserID: "actor-1", Source: quack.AuditSourceAPI, Action: "case.create", ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess, MetadataJSON: "{}"},
		{GuildID: guildID, ActorDiscordUserID: "actor-2", Source: quack.AuditSourceSystem, Action: "case_action.failed", ResourceType: "case_action_execution", ResourceID: "action-1", Result: quack.AuditResultFailure, MetadataJSON: "{}"},
		{GuildID: otherGuild.ID, ActorDiscordUserID: "actor-1", Source: quack.AuditSourceAPI, Action: "case.create", ResourceType: "case", ResourceID: "case-2", Result: quack.AuditResultSuccess, MetadataJSON: "{}"},
	}
	for i := range entries {
		if err := store.CreateAuditLogEntry(ctx, &entries[i]); err != nil {
			t.Fatalf("create audit %d: %v", i, err)
		}
	}

	list, err := store.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Limit: 10})
	if err != nil {
		t.Fatalf("list filtered audits: %v", err)
	}
	if list.Total != 2 || len(list.Entries) != 2 || list.Entries[0].Action != "case_action.failed" {
		t.Fatalf("expected newest-first guild audits, got %+v", list)
	}

	list, err = store.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{
		GuildID:            guildID,
		ActorDiscordUserID: "actor-1",
		Action:             "case.create",
		ResourceType:       "case",
		ResourceID:         "case-1",
		Result:             quack.AuditResultSuccess,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("list filtered audits with filters: %v", err)
	}
	if list.Total != 1 || len(list.Entries) != 1 || list.Entries[0].ResourceID != "case-1" {
		t.Fatalf("expected filtered case audit, got %+v", list)
	}
}

func TestActionQueueSnapshot(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	otherGuild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "guild-2",
		Name:               "Guild Two",
		OwnerDiscordUserID: "owner-2",
	})
	if err != nil {
		t.Fatalf("upsert other guild: %v", err)
	}

	created, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionSendDM, Status: quack.ActionExecutionPending, ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: quack.ActionBanUser, Status: quack.ActionExecutionFailed, ConfigSnapshotJSON: `{}`, LastErrorCode: "action_not_implemented", LastError: "ban_user action module is not implemented"},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}
	if _, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(otherGuild.ID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionSendDM, Status: quack.ActionExecutionPending, ConfigSnapshotJSON: `{}`},
		},
	}); err != nil {
		t.Fatalf("create other guild case: %v", err)
	}

	snapshot, err := store.ActionQueueSnapshot(ctx, guildID, 10)
	if err != nil {
		t.Fatalf("action queue snapshot: %v", err)
	}
	counts := map[quack.ActionExecutionStatus]int64{}
	for _, row := range snapshot.StatusCounts {
		counts[row.Status] = row.Count
	}
	if counts[quack.ActionExecutionPending] != 1 || counts[quack.ActionExecutionFailed] != 1 {
		t.Fatalf("unexpected status counts: %+v", snapshot.StatusCounts)
	}
	if snapshot.OldestPendingOrRetry == nil || snapshot.OldestPendingOrRetry.CaseID != created.Case.ID {
		t.Fatalf("expected oldest pending action for created case, got %+v", snapshot.OldestPendingOrRetry)
	}
	if len(snapshot.RecentFailures) != 1 || snapshot.RecentFailures[0].LastErrorCode != "action_not_implemented" {
		t.Fatalf("expected recent unsupported action failure, got %+v", snapshot.RecentFailures)
	}
}

func TestCreateCaseRollsBackOnActionFailure(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	_, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionTimeoutUser, IdempotencyKey: "duplicate-key", ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: quack.ActionKickUser, IdempotencyKey: "duplicate-key", ConfigSnapshotJSON: `{}`},
		},
		Audit: &quack.AuditLogEntry{
			GuildID:      guildID,
			Source:       quack.AuditSourceAPI,
			Action:       "case.create",
			ResourceType: "case",
			Result:       quack.AuditResultSuccess,
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

func TestCaseActionStateMachineClaimAndComplete(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionSendDM, ConfigSnapshotJSON: `{}`},
			{Position: 2, ActionType: quack.ActionTimeoutUser, ConfigSnapshotJSON: `{}`},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	claimed, err := store.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil {
		t.Fatalf("claim action: %v", err)
	}
	if claimed == nil || claimed.Execution.Position != 1 || claimed.Execution.AttemptCount != 1 || claimed.Execution.Status != quack.ActionExecutionRunning {
		t.Fatalf("unexpected claimed action: %+v", claimed)
	}

	if err := store.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            "worker-1",
		AttemptStatus:       quack.ActionAttemptFailed,
		ExecutionStatus:     quack.ActionExecutionFailed,
		ErrorCode:           "dm_closed",
		ErrorMessage:        "user cannot receive DMs",
		RequestPayloadJSON:  `{}`,
		ResponsePayloadJSON: `{}`,
		EventType:           quack.CaseEventActionFailed,
		EventBody:           "DM failed",
		EventMetadataJSON:   `{}`,
	}); err != nil {
		t.Fatalf("complete failed action: %v", err)
	}
	actions, err := store.ListCaseActionExecutions(ctx, created.Case.ID)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if actions[0].Status != quack.ActionExecutionFailed || actions[1].Status != quack.ActionExecutionPending {
		t.Fatalf("expected failed then pending actions, got %+v", actions)
	}

	var attempts []quack.CaseActionAttempt
	if err := store.DB().Where("execution_id = ?", claimed.Execution.ID).Find(&attempts).Error; err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(attempts) != 1 || attempts[0].Status != quack.ActionAttemptFailed || attempts[0].AttemptNumber != 1 {
		t.Fatalf("unexpected attempts: %+v", attempts)
	}
	audits, err := store.ListAuditLogEntries(ctx, guildID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if len(audits) != 2 || audits[0].Action != string(quack.AuditActionActionAttempt) || audits[1].Action != "case_action.failed" {
		t.Fatalf("expected action attempt and failure audits, got %+v", audits)
	}

	cases, err := store.ListCases(ctx, guildID)
	if err != nil {
		t.Fatalf("list cases: %v", err)
	}
	if cases[0].Validity != quack.CaseValidityValid {
		t.Fatalf("expected action failure not to change case validity, got %+v", cases[0])
	}
}

func TestCaseActionRetryScheduling(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)

	created, err := store.CreateCase(ctx, quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{
			{Position: 1, ActionType: quack.ActionSendDM, ConfigSnapshotJSON: `{}`, MaxRetries: 1, RetryBackoffMS: 100},
		},
	})
	if err != nil {
		t.Fatalf("create case: %v", err)
	}

	claimed, err := store.ClaimNextCaseAction(ctx, quack.ClaimCaseActionParams{CaseID: created.Case.ID, WorkerID: "worker-1"})
	if err != nil {
		t.Fatalf("claim action: %v", err)
	}
	nextRetryAt := time.Now().UTC().Add(time.Hour)
	if err := store.CompleteCaseAction(ctx, quack.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            "worker-1",
		AttemptStatus:       quack.ActionAttemptFailed,
		ExecutionStatus:     quack.ActionExecutionRetrying,
		ErrorCode:           "rate_limited",
		ErrorMessage:        "rate limited",
		RequestPayloadJSON:  `{}`,
		ResponsePayloadJSON: `{}`,
		NextRetryAt:         &nextRetryAt,
		EventType:           quack.CaseEventActionFailed,
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
	if err := store.DB().Model(&quack.CaseActionExecution{}).Where("id = ?", claimed.Execution.ID).Update("next_retry_at", past).Error; err != nil {
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

func TestListExecutableCaseIDsRotatesBoundedBatchesAcrossGuilds(t *testing.T) {
	ctx := context.Background()
	store, guildOneID := templateTestStore(t)
	guildTwo, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "fair-guild-2", Name: "Fair Two", OwnerDiscordUserID: "owner-2"})
	if err != nil {
		t.Fatalf("upsert second guild: %v", err)
	}
	guildThree, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "fair-guild-3", Name: "Fair Three", OwnerDiscordUserID: "owner-3"})
	if err != nil {
		t.Fatalf("upsert third guild: %v", err)
	}

	caseGuilds := map[string]string{}
	base := time.Now().UTC().Add(-time.Hour)
	for index := range 5 {
		caseID := createExecutableFairnessCase(t, store, guildOneID, 1, base.Add(time.Duration(index)*time.Minute))
		caseGuilds[caseID] = guildOneID
	}
	caseGuilds[createExecutableFairnessCase(t, store, guildTwo.ID, 1, base.Add(10*time.Minute))] = guildTwo.ID
	caseGuilds[createExecutableFairnessCase(t, store, guildThree.ID, 1, base.Add(20*time.Minute))] = guildThree.ID

	seenGuilds := map[string]bool{}
	for poll := range 2 {
		caseIDs, listErr := store.ListExecutableCaseIDs(ctx, 2)
		if listErr != nil {
			t.Fatalf("poll %d: %v", poll, listErr)
		}
		if len(caseIDs) != 2 {
			t.Fatalf("poll %d expected bounded pair, got %+v", poll, caseIDs)
		}
		firstGuild := caseGuilds[caseIDs[0]]
		secondGuild := caseGuilds[caseIDs[1]]
		if firstGuild == "" || secondGuild == "" || firstGuild == secondGuild {
			t.Fatalf("poll %d was not guild-fair: ids=%+v guilds=%q,%q", poll, caseIDs, firstGuild, secondGuild)
		}
		seenGuilds[firstGuild] = true
		seenGuilds[secondGuild] = true
	}
	for _, guildID := range []string{guildOneID, guildTwo.ID, guildThree.ID} {
		if !seenGuilds[guildID] {
			t.Fatalf("bounded rotating polls starved guild %s: seen=%+v", guildID, seenGuilds)
		}
	}
}

func TestListExecutableCaseIDsPreservesPriorityWithinGuild(t *testing.T) {
	ctx := context.Background()
	store, guildID := templateTestStore(t)
	lowPriority := createExecutableFairnessCase(t, store, guildID, 2, time.Now().UTC().Add(-time.Hour))
	highPriority := createExecutableFairnessCase(t, store, guildID, 1, time.Now().UTC())

	caseIDs, err := store.ListExecutableCaseIDs(ctx, 2)
	if err != nil {
		t.Fatalf("list executable cases: %v", err)
	}
	if len(caseIDs) != 2 || caseIDs[0] != highPriority || caseIDs[1] != lowPriority {
		t.Fatalf("expected position priority within guild, got %+v", caseIDs)
	}
}

func createExecutableFairnessCase(t *testing.T, store *storage.Store, guildID string, position int, readyAt time.Time) string {
	t.Helper()
	created, err := store.CreateCase(context.Background(), quack.CreateCaseParams{
		Case:  caseModel(guildID, nil),
		Event: caseEvent(),
		ActionExecutions: []quack.CaseActionExecution{{
			Position: position, ActionType: quack.ActionTimeoutUser, ConfigSnapshotJSON: `{}`,
		}},
	})
	if err != nil {
		t.Fatalf("create executable fairness case: %v", err)
	}
	if err := store.DB().Model(&quack.CaseActionExecution{}).Where("case_id = ?", created.Case.ID).Update("created_at", readyAt).Error; err != nil {
		t.Fatalf("set executable fairness priority: %v", err)
	}
	return created.Case.ID
}

func createCaseStorageTemplate(t *testing.T, store *storage.Store, guildID string) *quack.ExpandedCaseTemplate {
	t.Helper()

	created, err := store.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: templateModel(guildID, "spam"),
		Levels: []quack.ExpandedCaseTemplateLevel{
			{
				Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
				Actions: []quack.CaseTemplateLevelAction{
					{ActionType: quack.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	return created
}

func caseModel(guildID string, templateID *string) quack.Case {
	return quack.Case{
		GuildID:                guildID,
		TemplateID:             templateID,
		TemplateVersion:        1,
		TemplateSnapshotJSON:   "{}",
		TargetDiscordUserID:    "target-1",
		ModeratorDiscordUserID: "moderator-1",
		Reason:                 "No spam",
		Validity:               quack.CaseValidityValid,
		Source:                 quack.CaseSourceDashboard,
		MetadataJSON:           "{}",
	}
}

func caseEvent() quack.CaseEvent {
	return quack.CaseEvent{
		EventType:          quack.CaseEventCreated,
		ActorDiscordUserID: "moderator-1",
		Body:               "Case created",
		MetadataJSON:       "{}",
	}
}
