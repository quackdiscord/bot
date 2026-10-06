package quack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// attemptTimeout bounds one Discord call so a hung request can't hold the
// lease until it expires.
const attemptTimeout = 30 * time.Second

// ActionService runs case enforcement and the staff controls around it:
// retrying or dismissing failures and reversing timeouts and bans.
type ActionService struct {
	store     ActionStore
	enforcer  Enforcer
	messenger Messenger
	// guilds re-checks Discord before staff retries and reversals.
	guilds    *GuildService
	scheduler Scheduler
	// dashboard builds the appeal link in case notifications.
	dashboard DashboardLinks
}

// NewActionService returns an ActionService. enforcer, messenger, and
// scheduler may be nil: actions then fail for review, notifications fail,
// and requeued work waits for the poller. dashboardURL is the dashboard
// that appealable case notifications link to (see NewDashboardLinks); ""
// leaves the link out.
func NewActionService(store ActionStore, enforcer Enforcer, messenger Messenger, guilds *GuildService, scheduler Scheduler, dashboardURL string) *ActionService {
	return &ActionService{
		store:     store,
		enforcer:  enforcer,
		messenger: messenger,
		guilds:    guilds,
		scheduler: scheduler,
		dashboard: NewDashboardLinks(dashboardURL),
	}
}

// ProcessCaseActions runs a case's due executions in order, then sends its
// notification once nothing is left to run. Workers call it for each
// scheduled or polled case; it is safe to call concurrently because each
// execution is leased.
func (s *ActionService) ProcessCaseActions(ctx context.Context, caseID string) error {
	ctx = ensureTraceContext(ctx)
	caseID = strings.TrimSpace(caseID)
	workerID := fmt.Sprintf("action-worker:%d", time.Now().UTC().UnixNano())
	for {
		claimed, err := s.store.ClaimNextCaseAction(ctx, ClaimCaseActionParams{CaseID: caseID, WorkerID: workerID})
		if err != nil {
			return err
		}
		if claimed == nil {
			return s.sendNotification(ctx, workerID, caseID)
		}
		// Kicks and bans remove the shared guild Quack needs to open a DM, so
		// open it first.
		if claimed.Execution.ActionType == ActionKickUser || claimed.Execution.ActionType == ActionBanUser {
			s.prepareNotification(ctx, claimed.Case)
		}
		if err := s.attempt(ctx, workerID, *claimed); err != nil {
			return err
		}
	}
}

// attempt performs one leased execution and records the outcome.
func (s *ActionService) attempt(ctx context.Context, workerID string, claimed ClaimedCaseAction) error {
	item, execution := claimed.Case, claimed.Execution
	config := decodeActionConfig(execution.ConfigSnapshotJSON)
	logger := slog.With("case_id", item.ID, "guild_id", item.GuildID,
		"execution_id", execution.ID, "action", execution.ActionType,
		"attempt", execution.AttemptCount)
	logger.InfoContext(ctx, "Action attempt started")

	var result attemptResult
	guild, err := s.store.GetGuildByID(ctx, item.GuildID)
	switch {
	case err != nil:
		// Nothing reached Discord, so retrying is safe.
		result = retryableFailure("guild_lookup_failed", "Guild information is temporarily unavailable")
	case guild == nil || guild.DiscordGuildID == "":
		result = permanentFailure("guild_not_found", "The case guild is unavailable")
	default:
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		result = s.enforce(attemptCtx, guild.DiscordGuildID, item, execution, config)
		cancel()
	}

	requestID, correlationID := TraceIDsFromContext(ctx)
	if correlationID == "" {
		correlationID = item.CorrelationID
	}
	attemptStatus := ActionAttemptSucceeded
	executionStatus := ActionExecutionSucceeded
	eventType := CaseEventActionSucceeded
	eventBody := "Discord enforcement succeeded"
	if noop, _ := result.Response["reversal_noop"].(bool); noop && result.Error == "" {
		eventBody = "Punishment was already over; no reversal request was sent"
	}
	var nextRetryAt *time.Time
	if result.Error != "" {
		attemptStatus = ActionAttemptFailed
		executionStatus = ActionExecutionFailed
		eventType = CaseEventActionFailed
		eventBody = "Discord enforcement failed and requires staff review"
		if shouldRetry(execution, result) {
			next := nextRetryTime(execution)
			nextRetryAt = &next
			executionStatus = ActionExecutionRetrying
			eventBody = "Discord enforcement is waiting for a safe automatic retry"
		}
	}
	if result.Response == nil {
		result.Response = map[string]any{}
	}
	if result.Error != "" {
		result.Response["error"] = result.Error
	}

	err = s.store.CompleteCaseAction(ctx, CompleteCaseActionParams{
		ExecutionID:     execution.ID,
		LeaseToken:      execution.LeaseToken,
		AttemptNumber:   execution.AttemptCount,
		WorkerID:        workerID,
		AttemptStatus:   attemptStatus,
		ExecutionStatus: executionStatus,
		ErrorCode:       result.ErrorCode,
		ErrorMessage:    result.Error,
		RequestPayloadJSON: marshalJSONObject(map[string]any{
			"case_id":      item.ID,
			"execution_id": execution.ID,
			"action_type":  execution.ActionType,
			"config":       config,
		}),
		ResponsePayloadJSON: marshalJSONObject(result.Response),
		NextRetryAt:         nextRetryAt,
		EventType:           eventType,
		EventBody:           eventBody,
		EventMetadataJSON: marshalJSONObject(map[string]any{
			"execution_id": execution.ID,
			"action_type":  execution.ActionType,
			"retrying":     executionStatus == ActionExecutionRetrying,
		}),
		CorrelationID: correlationID,
		RequestID:     requestID,
	})
	if err != nil {
		return fmt.Errorf("record action result: %w", err)
	}
	level := slog.LevelInfo
	if result.Error != "" {
		level = slog.LevelWarn
	}
	logger.Log(ctx, level, "Action attempt recorded", "status", executionStatus,
		"error_code", result.ErrorCode, "outcome_uncertain", result.OutcomeUncertain,
		"next_retry_at", nextRetryAt)
	return nil
}

// shouldRetry allows another automatic attempt only when Discord definitely
// did not apply the request, the execution is safe to repeat, and retries
// remain. Everything else waits for staff.
func shouldRetry(execution CaseActionExecution, result attemptResult) bool {
	if !result.Retryable || result.OutcomeUncertain || !execution.SafeForRetry {
		return false
	}
	return execution.AttemptCount <= execution.MaxRetries
}

// nextRetryTime applies the execution's backoff, defaulting to one second.
func nextRetryTime(execution CaseActionExecution) time.Time {
	backoff := execution.RetryBackoffMS
	if backoff <= 0 {
		backoff = defaultRetryBackoffMS
	}
	return time.Now().UTC().Add(time.Duration(backoff) * time.Millisecond)
}

// ListFailures returns a page of the guild's failed executions awaiting
// review. Reads are not audited.
func (s *ActionService) ListFailures(ctx context.Context, guildContext *GuildStaffContext, limit, offset int) (*FailedCaseActionResult, error) {
	if guildContext == nil || guildContext.Guild == nil || !guildContext.Can(PermissionActionCaseRead) {
		return nil, ErrCasePermissionDenied
	}
	return s.store.ListFailedCaseActions(ctx, FailedCaseActionFilter{
		GuildID: guildContext.Guild.ID,
		Limit:   limit,
		Offset:  offset,
	})
}

// Retry requeues a failed execution after re-checking Discord. Staff use it
// once they have confirmed the first attempt did not take effect.
func (s *ActionService) Retry(ctx context.Context, guildContext *GuildStaffContext, executionID string) (updated *CaseActionExecution, err error) {
	defer func() { s.auditControlFailure(ctx, guildContext, string(AuditActionActionRetry), executionID, err) }()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, ErrCasePermissionDenied
	}
	execution, err := s.store.GetCaseActionExecution(ctx, guildContext.Guild.ID, executionID)
	if err != nil {
		return nil, err
	}
	if execution == nil || execution.DismissedAt != nil {
		return nil, ErrCaseNotFound
	}
	if execution.Status == ActionExecutionPending || execution.Status == ActionExecutionRetrying {
		return execution, nil
	}
	if execution.Status != ActionExecutionFailed {
		return nil, ErrCaseNotFound
	}
	item, err := s.store.GetCaseByID(ctx, execution.CaseID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	if s.guilds == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if err := s.guilds.PreflightCase(ctx, guildContext, item.TargetDiscordUserID, execution.ActionType); err != nil {
		return nil, err
	}
	updated, err = s.store.RetryCaseAction(ctx, RetryCaseActionParams{
		GuildID:            item.GuildID,
		ExecutionID:        execution.ID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Audit:              controlAudit(ctx, guildContext, string(AuditActionActionRetry), execution.ID),
	})
	if err == nil && updated != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return updated, err
}

// Dismiss removes a failed execution from the review queue. Its attempts
// stay on record.
func (s *ActionService) Dismiss(ctx context.Context, guildContext *GuildStaffContext, executionID string) (updated *CaseActionExecution, err error) {
	defer func() { s.auditControlFailure(ctx, guildContext, string(AuditActionActionDismiss), executionID, err) }()
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil ||
		!guildContext.Can(PermissionActionFailureDismiss) {
		return nil, ErrCasePermissionDenied
	}
	return s.store.DismissCaseAction(ctx, DismissCaseActionParams{
		GuildID:            guildContext.Guild.ID,
		ExecutionID:        executionID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Audit:              controlAudit(ctx, guildContext, string(AuditActionActionDismiss), executionID),
	})
}

// Reverse queues the reversal of a succeeded timeout or ban.
func (s *ActionService) Reverse(ctx context.Context, guildContext *GuildStaffContext, caseID, originalExecutionID string, actionType ActionType) (*CaseActionExecution, error) {
	return s.ReverseForAppeal(ctx, guildContext, caseID, originalExecutionID, actionType, nil)
}

// ReverseForAppeal queues the reversal of a succeeded timeout or ban, linked
// to appealID when the reversal follows an accepted appeal. Reversals are
// never retried automatically.
func (s *ActionService) ReverseForAppeal(ctx context.Context, guildContext *GuildStaffContext, caseID, originalExecutionID string, actionType ActionType, appealID *string) (queued *CaseActionExecution, err error) {
	defer func() {
		s.auditControlFailure(ctx, guildContext, string(AuditActionActionReverse), originalExecutionID, err)
	}()
	if s.guilds == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, ErrCaseNotFound
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrCaseNotFound
	}
	if err := s.guilds.PreflightReversal(ctx, guildContext, item.TargetDiscordUserID, actionType); err != nil {
		return nil, err
	}
	original, err := s.store.GetCaseActionExecution(ctx, item.GuildID, originalExecutionID)
	if err != nil {
		return nil, err
	}
	if original == nil || original.CaseID != item.ID {
		return nil, ErrCaseNotFound
	}
	if err := s.checkReversal(ctx, item, *original, actionType, appealID); err != nil {
		return nil, err
	}
	queued, err = s.store.QueueCaseReversal(ctx, QueueCaseReversalParams{
		GuildID:             item.GuildID,
		CaseID:              item.ID,
		ActorDiscordUserID:  guildContext.Staff.DiscordUserID,
		OriginalExecutionID: original.ID,
		ActionType:          actionType,
		AppealID:            appealID,
		Audit:               controlAudit(ctx, guildContext, string(AuditActionActionReverse), originalExecutionID),
	})
	if err == nil && queued != nil && s.scheduler != nil {
		s.scheduler.Submit(ctx, item.ID)
	}
	return queued, err
}

// reversalOf maps an action to the action that undoes it. Kicks cannot be
// undone, and reversals are not themselves reversible.
func reversalOf(actionType ActionType) (ActionType, bool) {
	switch actionType {
	case ActionTimeoutUser:
		return ActionRemoveTimeout, true
	case ActionBanUser:
		return ActionUnbanUser, true
	default:
		return "", false
	}
}

// checkReversal decides whether original may be reversed with actionType:
// it must have succeeded, the reversal must match it, and an appeal-linked
// reversal needs that appeal accepted for this case.
func (s *ActionService) checkReversal(ctx context.Context, item *Case, original CaseActionExecution, actionType ActionType, appealID *string) error {
	if original.Status != ActionExecutionSucceeded {
		return errors.New("only a succeeded action can be reversed")
	}
	if want, ok := reversalOf(original.ActionType); !ok || want != actionType {
		return errors.New("reversal does not match original action")
	}
	if appealID == nil {
		return nil
	}
	appeal, err := s.store.GetAppealByID(ctx, *appealID)
	if err != nil {
		return err
	}
	if appeal == nil || appeal.CaseID == nil || *appeal.CaseID != item.ID || appeal.Status != AppealStatusAccepted {
		return errors.New("reversal appeal is not accepted for this case")
	}
	return nil
}

// auditControlFailure records a failed staff control, or one Quack's own
// Discord access denied. Successes are audited by the store in the same
// transaction as the change; denials about the actor are not audited.
func (s *ActionService) auditControlFailure(ctx context.Context, guildContext *GuildStaffContext, action, executionID string, err error) {
	if err == nil || isActorDenial(err) {
		return
	}
	result := AuditResultFailure
	if errors.Is(err, ErrAuthorizationDenied) {
		result = AuditResultDenied
	}
	_ = s.audit(ctx, guildContext, action, executionID, result, err.Error())
}

func (s *ActionService) audit(ctx context.Context, guildContext *GuildStaffContext, action, executionID string, result AuditResult, failureReason string) error {
	return recordStaffAudit(ctx, s.store, guildContext, action, "case_action_execution", executionID, result, failureReason)
}

// controlAudit is the success entry the store writes with a staff control.
func controlAudit(ctx context.Context, guildContext *GuildStaffContext, action, executionID string) *AuditLogEntry {
	return staffAudit(ctx, guildContext, action, "case_action_execution", executionID, AuditResultSuccess, "")
}
