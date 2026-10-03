package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// errStaleLease means a worker tried to finish work whose lease it no longer
// holds, because the lease expired and the work was recovered or reassigned.
var errStaleLease = errors.New("lease is no longer held")

// activeExecutionStatuses are the states that still have enforcement to do.
// A case's notification waits until none of its executions is in one.
var activeExecutionStatuses = []quack.ActionExecutionStatus{
	quack.ActionExecutionPending,
	quack.ActionExecutionRunning,
	quack.ActionExecutionRetrying,
}

// ClaimNextCaseAction leases the case's next due execution and opens an
// attempt for it, or returns nil when nothing is due or another worker holds
// a live lease on the case. An execution whose lease expired is never retried
// here: Discord may already have applied it, so it fails for staff review.
func (s *Store) ClaimNextCaseAction(ctx context.Context, params quack.ClaimCaseActionParams) (*quack.ClaimedCaseAction, error) {
	now := time.Now().UTC()
	var claimed *quack.ClaimedCaseAction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c caseRecord
		found, err := first(forUpdate(tx).Where("id = ?", params.CaseID), &c)
		if err != nil || !found {
			return wrap("get case for action claim", err)
		}
		var running int64
		if err := tx.Model(&executionRecord{}).
			Where("case_id = ? AND status = ? AND lease_expires_at > ?", c.ID, quack.ActionExecutionRunning, now).
			Count(&running).Error; err != nil {
			return fmt.Errorf("count running case actions: %w", err)
		}
		if running > 0 {
			return nil
		}
		var e executionRecord
		found, err = first(forUpdate(tx).
			Where("case_id = ?", c.ID).
			Where("(status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND lease_expires_at <= ?)",
				[]quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRetrying}, now,
				quack.ActionExecutionRunning, now).
			Order("position ASC"), &e)
		if err != nil || !found {
			return wrap("claim case action", err)
		}
		if e.Status == quack.ActionExecutionRunning {
			return failExpiredAction(tx, c, &e, now)
		}

		e.Status = quack.ActionExecutionRunning
		e.AttemptCount++
		e.StartedAt = &now
		e.FinishedAt = nil
		e.NextRetryAt = nil
		e.LeaseToken = quack.NewID()
		expires := now.Add(leaseDuration)
		e.LeaseExpiresAt = &expires
		e.UpdatedAt = now
		if err := tx.Save(&e).Error; err != nil {
			return fmt.Errorf("mark case action running: %w", err)
		}
		attempt := attemptRecord{
			ID:                  quack.NewID(),
			CreatedAt:           now,
			UpdatedAt:           now,
			ExecutionID:         e.ID,
			AttemptNumber:       e.AttemptCount,
			Status:              quack.ActionAttemptRunning,
			WorkerID:            params.WorkerID,
			StartedAt:           now,
			RequestPayloadJSON:  "{}",
			ResponsePayloadJSON: "{}",
		}
		if err := tx.Create(&attempt).Error; err != nil {
			return fmt.Errorf("create action attempt: %w", err)
		}
		if err := createAuditLogEntry(tx, &quack.AuditLogEntry{
			GuildID:       c.GuildID,
			Source:        quack.AuditSourceSystem,
			Action:        string(quack.AuditActionActionAttempt),
			ResourceType:  "case_action_execution",
			ResourceID:    e.ID,
			Result:        quack.AuditResultSuccess,
			CorrelationID: cmp.Or(e.CorrelationID, c.CorrelationID),
			MetadataJSON: jsonObject(map[string]any{
				"case_id":        c.ID,
				"attempt_number": attempt.AttemptNumber,
				"status":         attempt.Status,
			}),
		}, now); err != nil {
			return err
		}
		claimed = &quack.ClaimedCaseAction{Case: c.model(), Execution: e.model()}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

// failExpiredAction handles an execution whose worker stopped mid-attempt. It
// closes the open attempt, fails the execution into the staff review queue,
// and fences the old worker by clearing its lease.
func failExpiredAction(tx *gorm.DB, c caseRecord, e *executionRecord, now time.Time) error {
	result := tx.Model(&attemptRecord{}).
		Where("execution_id = ? AND attempt_number = ? AND status = ?", e.ID, e.AttemptCount, quack.ActionAttemptRunning).
		Updates(map[string]any{
			"status":        quack.ActionAttemptFailed,
			"finished_at":   now,
			"error_code":    "lease_expired",
			"error_message": "worker lease expired before completion",
			"updated_at":    now,
		})
	if result.Error != nil {
		return fmt.Errorf("close expired action attempt: %w", result.Error)
	}
	e.Status = quack.ActionExecutionFailed
	e.LastErrorCode = "lease_expired_review_required"
	e.LastError = "Worker stopped before recording the result; confirm the Discord outcome before retrying"
	e.FinishedAt = &now
	e.UpdatedAt = now
	e.LeaseToken = ""
	e.LeaseExpiresAt = nil
	e.NextRetryAt = nil
	if err := tx.Save(e).Error; err != nil {
		return fmt.Errorf("fail expired action: %w", err)
	}
	if err := appendCaseEvent(tx, &quack.CaseEvent{
		CaseID:       c.ID,
		GuildID:      c.GuildID,
		EventType:    quack.CaseEventActionFailed,
		Visibility:   quack.EventVisibilityPublic,
		Body:         "Discord enforcement could not be confirmed and requires staff review",
		MetadataJSON: jsonObject(map[string]any{"execution_id": e.ID}),
	}, now); err != nil {
		return err
	}
	return createAuditLogEntry(tx, &quack.AuditLogEntry{
		GuildID:       c.GuildID,
		Source:        quack.AuditSourceSystem,
		Action:        string(quack.AuditActionActionRecovered),
		ResourceType:  "case_action_execution",
		ResourceID:    e.ID,
		Result:        quack.AuditResultFailure,
		FailureReason: e.LastErrorCode,
		CorrelationID: cmp.Or(e.CorrelationID, c.CorrelationID),
		MetadataJSON: jsonObject(map[string]any{
			"case_id":        c.ID,
			"attempt_number": e.AttemptCount,
			"recovery":       "staff_review_required",
		}),
	}, now)
}

// CompleteCaseAction records the outcome of a leased attempt. It fails with a
// stale-lease error unless params.LeaseToken still holds the lease on a
// running execution, so a worker whose lease expired cannot overwrite the
// recovery decision.
func (s *Store) CompleteCaseAction(ctx context.Context, params quack.CompleteCaseActionParams) error {
	if params.LeaseToken == "" {
		return fmt.Errorf("complete case action: %w", errStaleLease)
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var e executionRecord
		found, err := first(forUpdate(tx).Where("id = ? AND lease_token = ? AND status = ?",
			params.ExecutionID, params.LeaseToken, quack.ActionExecutionRunning), &e)
		if err != nil {
			return fmt.Errorf("get case action execution: %w", err)
		}
		if !found {
			return fmt.Errorf("complete case action: %w", errStaleLease)
		}
		var c caseRecord
		found, err = first(tx.Where("id = ?", e.CaseID), &c)
		if err != nil {
			return fmt.Errorf("get case for action result: %w", err)
		}
		if !found {
			return fmt.Errorf("get case for action result: case %s not found", e.CaseID)
		}

		attemptNumber := cmp.Or(params.AttemptNumber, e.AttemptCount)
		var attempt attemptRecord
		found, err = first(forUpdate(tx).
			Where("execution_id = ? AND attempt_number = ?", e.ID, attemptNumber), &attempt)
		if err != nil {
			return fmt.Errorf("get action attempt: %w", err)
		}
		if !found {
			startedAt := now
			if e.StartedAt != nil {
				startedAt = *e.StartedAt
			}
			attempt = attemptRecord{
				ID:            quack.NewID(),
				CreatedAt:     now,
				ExecutionID:   e.ID,
				AttemptNumber: attemptNumber,
				StartedAt:     startedAt,
				WorkerID:      params.WorkerID,
			}
		}
		attempt.Status = params.AttemptStatus
		attempt.FinishedAt = &now
		attempt.DurationMS = now.Sub(attempt.StartedAt).Milliseconds()
		attempt.ErrorCode = params.ErrorCode
		attempt.ErrorMessage = params.ErrorMessage
		attempt.RequestPayloadJSON = cmp.Or(params.RequestPayloadJSON, "{}")
		attempt.ResponsePayloadJSON = cmp.Or(params.ResponsePayloadJSON, "{}")
		attempt.UpdatedAt = now
		if err := tx.Save(&attempt).Error; err != nil {
			return fmt.Errorf("complete action attempt: %w", err)
		}

		e.Status = params.ExecutionStatus
		e.LastErrorCode = params.ErrorCode
		e.LastError = params.ErrorMessage
		e.FinishedAt = &now
		if params.ExecutionStatus == quack.ActionExecutionRetrying {
			e.FinishedAt = nil
		}
		e.NextRetryAt = params.NextRetryAt
		e.LeaseToken = ""
		e.LeaseExpiresAt = nil
		e.UpdatedAt = now
		if err := tx.Save(&e).Error; err != nil {
			return fmt.Errorf("update case action execution: %w", err)
		}

		if params.EventType != "" {
			if err := appendCaseEvent(tx, &quack.CaseEvent{
				CaseID:       c.ID,
				GuildID:      c.GuildID,
				EventType:    params.EventType,
				Visibility:   quack.EventVisibilityPublic,
				Body:         params.EventBody,
				MetadataJSON: params.EventMetadataJSON,
			}, now); err != nil {
				return err
			}
		}

		action, result := "case_action.succeeded", quack.AuditResultSuccess
		switch params.ExecutionStatus {
		case quack.ActionExecutionRetrying:
			action, result = "case_action.retrying", quack.AuditResultFailure
		case quack.ActionExecutionFailed:
			action, result = "case_action.failed", quack.AuditResultFailure
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{
			GuildID:       c.GuildID,
			Source:        quack.AuditSourceSystem,
			Action:        action,
			ResourceType:  "case_action_execution",
			ResourceID:    e.ID,
			Result:        result,
			FailureReason: params.ErrorMessage,
			CorrelationID: cmp.Or(params.CorrelationID, e.CorrelationID, c.CorrelationID),
			RequestID:     params.RequestID,
			MetadataJSON: jsonObject(map[string]any{
				"case_id":        c.ID,
				"case_number":    c.CaseNumber,
				"action_type":    e.ActionType,
				"attempt_number": attemptNumber,
				"retrying":       params.ExecutionStatus == quack.ActionExecutionRetrying,
			}),
		}, now)
	})
}

// ListExecutableCaseIDs returns up to limit cases with work due: a due or
// abandoned execution, or a member notification ready to send once the
// case's enforcement has settled. The poller hands each to the action worker.
//
// The batch is fair across guilds. Every guild with due work gets one slot
// before any guild gets a second, and the starting guild rotates between
// calls, so one busy guild cannot starve the rest. Claiming stays the real
// guard against double work; this only picks candidates.
func (s *Store) ListExecutableCaseIDs(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 100
	}
	s.pollMu.Lock()
	defer s.pollMu.Unlock()

	now := time.Now().UTC()
	const query = `
WITH due AS (
    SELECT e.case_id, c.guild_id,
           MIN(e.position) AS first_position,
           MIN(COALESCE(e.next_retry_at, e.lease_expires_at, e.created_at)) AS ready_at
      FROM case_action_executions e
      JOIN cases c ON c.id = e.case_id
     WHERE (e.status IN (@pending, @retrying) AND (e.next_retry_at IS NULL OR e.next_retry_at <= @now))
        OR (e.status = @running AND e.lease_expires_at <= @now)
     GROUP BY e.case_id, c.guild_id
    UNION ALL
    SELECT n.case_id, c.guild_id, 0 AS first_position, COALESCE(n.lease_expires_at, n.updated_at) AS ready_at
      FROM case_notifications n
      JOIN cases c ON c.id = n.case_id
     WHERE (n.status IN (@notifyPending, @notifyPrepared) OR (n.status = @notifyClaimed AND n.lease_expires_at <= @now))
       AND NOT EXISTS (SELECT 1 FROM case_action_executions e
                        WHERE e.case_id = n.case_id AND e.status IN (@pending, @running, @retrying))
), ranked AS (
    SELECT case_id, guild_id, first_position, ready_at,
           ROW_NUMBER() OVER (PARTITION BY guild_id ORDER BY first_position, ready_at, case_id) AS guild_rank
      FROM due
)
SELECT case_id, guild_id
  FROM ranked
 ORDER BY guild_rank,
          CASE WHEN guild_id > @cursor THEN 0 ELSE 1 END,
          guild_id, first_position, ready_at, case_id
 LIMIT @limit`
	var rows []struct{ CaseID, GuildID string }
	err := s.db.WithContext(ctx).Raw(query, map[string]any{
		"pending":        quack.ActionExecutionPending,
		"retrying":       quack.ActionExecutionRetrying,
		"running":        quack.ActionExecutionRunning,
		"notifyPending":  quack.NotificationPending,
		"notifyPrepared": quack.NotificationPrepared,
		"notifyClaimed":  quack.NotificationClaimed,
		"now":            now,
		"cursor":         s.pollCursor,
		"limit":          limit,
	}).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list executable case ids: %w", err)
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.CaseID
	}
	if len(rows) > 0 {
		s.pollCursor = rows[len(rows)-1].GuildID
	}
	return ids, nil
}

func newExecutionRecord(e quack.CaseActionExecution) executionRecord {
	return executionRecord{
		ID:                       e.ID,
		CreatedAt:                e.CreatedAt,
		UpdatedAt:                e.UpdatedAt,
		CaseID:                   e.CaseID,
		TemplateActionID:         e.TemplateActionID,
		Position:                 e.Position,
		ActionType:               e.ActionType,
		Status:                   e.Status,
		IdempotencyKey:           e.IdempotencyKey,
		ConfigSnapshotJSON:       e.ConfigSnapshotJSON,
		NotifyUser:               e.NotifyUser,
		NotificationType:         e.NotificationType,
		AttemptCount:             e.AttemptCount,
		MaxRetries:               e.MaxRetries,
		RetryBackoffMS:           e.RetryBackoffMS,
		SafeForRetry:             e.SafeForRetry,
		LastErrorCode:            e.LastErrorCode,
		LastError:                e.LastError,
		StartedAt:                e.StartedAt,
		FinishedAt:               e.FinishedAt,
		NextRetryAt:              e.NextRetryAt,
		CorrelationID:            e.CorrelationID,
		LeaseToken:               e.LeaseToken,
		LeaseExpiresAt:           e.LeaseExpiresAt,
		DismissedAt:              e.DismissedAt,
		DismissedByDiscordUserID: e.DismissedByDiscordUserID,
		ReversalOfExecutionID:    e.ReversalOfExecutionID,
		ReversalAppealID:         e.ReversalAppealID,
	}
}

func (r executionRecord) model() quack.CaseActionExecution {
	return quack.CaseActionExecution{
		ULIDModel:                ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		CaseID:                   r.CaseID,
		TemplateActionID:         r.TemplateActionID,
		Position:                 r.Position,
		ActionType:               r.ActionType,
		Status:                   r.Status,
		IdempotencyKey:           r.IdempotencyKey,
		ConfigSnapshotJSON:       r.ConfigSnapshotJSON,
		NotifyUser:               r.NotifyUser,
		NotificationType:         r.NotificationType,
		AttemptCount:             r.AttemptCount,
		MaxRetries:               r.MaxRetries,
		RetryBackoffMS:           r.RetryBackoffMS,
		SafeForRetry:             r.SafeForRetry,
		LastErrorCode:            r.LastErrorCode,
		LastError:                r.LastError,
		StartedAt:                r.StartedAt,
		FinishedAt:               r.FinishedAt,
		NextRetryAt:              r.NextRetryAt,
		CorrelationID:            r.CorrelationID,
		LeaseToken:               r.LeaseToken,
		LeaseExpiresAt:           r.LeaseExpiresAt,
		DismissedAt:              r.DismissedAt,
		DismissedByDiscordUserID: r.DismissedByDiscordUserID,
		ReversalOfExecutionID:    r.ReversalOfExecutionID,
		ReversalAppealID:         r.ReversalAppealID,
	}
}

func (r attemptRecord) model() quack.CaseActionAttempt {
	return quack.CaseActionAttempt{
		ULIDModel:           ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		ExecutionID:         r.ExecutionID,
		AttemptNumber:       r.AttemptNumber,
		Status:              r.Status,
		WorkerID:            r.WorkerID,
		StartedAt:           r.StartedAt,
		FinishedAt:          r.FinishedAt,
		DurationMS:          r.DurationMS,
		ErrorCode:           r.ErrorCode,
		ErrorMessage:        r.ErrorMessage,
		RequestPayloadJSON:  r.RequestPayloadJSON,
		ResponsePayloadJSON: r.ResponsePayloadJSON,
	}
}
