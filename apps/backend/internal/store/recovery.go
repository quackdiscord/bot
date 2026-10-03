package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// errNotFailed means staff tried to retry or dismiss an execution that is not
// in the failure queue.
var errNotFailed = errors.New("action is not failed")

// ListFailedCaseActions returns a page of a guild's failed, undismissed
// executions, most recently updated first: the staff review queue.
func (s *Store) ListFailedCaseActions(ctx context.Context, filter quack.FailedCaseActionFilter) (*quack.FailedCaseActionResult, error) {
	limit, offset := page(filter.Limit, filter.Offset)
	query := s.db.WithContext(ctx).Model(&executionRecord{}).
		Where("status = ? AND dismissed_at IS NULL", quack.ActionExecutionFailed).
		Where("case_id IN (SELECT id FROM cases WHERE guild_id = ?)", filter.GuildID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count failed case actions: %w", err)
	}
	var records []executionRecord
	if err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list failed case actions: %w", err)
	}
	return &quack.FailedCaseActionResult{Executions: modelsOf(records, executionRecord.model), Total: total}, nil
}

// RetryCaseAction requeues a failed execution after staff confirmed it is
// safe to run again. Retrying a queued execution is a no-op. It returns nil
// when the execution is not in the guild.
func (s *Store) RetryCaseAction(ctx context.Context, params quack.RetryCaseActionParams) (*quack.CaseActionExecution, error) {
	return s.reviewCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, e *executionRecord, now time.Time) error {
		if e.Status == quack.ActionExecutionPending || e.Status == quack.ActionExecutionRetrying {
			return nil
		}
		if e.Status != quack.ActionExecutionFailed {
			return errNotFailed
		}
		e.Status = quack.ActionExecutionPending
		e.NextRetryAt = nil
		e.StartedAt = nil
		e.FinishedAt = nil
		e.LeaseToken = ""
		e.LeaseExpiresAt = nil
		e.DismissedAt = nil
		e.DismissedByDiscordUserID = ""
		e.LastErrorCode = ""
		e.LastError = ""
		return saveReview(tx, e, quack.CaseEventActionRetried, "Action retry requested",
			params.ActorDiscordUserID, params.Audit, now)
	})
}

// DismissCaseAction removes a failed execution from the review queue. Its
// attempts stay as history. Dismissing twice is a no-op. It returns nil when
// the execution is not in the guild.
func (s *Store) DismissCaseAction(ctx context.Context, params quack.DismissCaseActionParams) (*quack.CaseActionExecution, error) {
	return s.reviewCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, e *executionRecord, now time.Time) error {
		if e.DismissedAt != nil {
			return nil
		}
		if e.Status != quack.ActionExecutionFailed {
			return errNotFailed
		}
		e.DismissedAt = &now
		e.DismissedByDiscordUserID = params.ActorDiscordUserID
		return saveReview(tx, e, quack.CaseEventActionDismissed, "Action failure dismissed",
			params.ActorDiscordUserID, params.Audit, now)
	})
}

// reviewCaseAction locks one of a guild's executions and applies a staff
// review decision to it.
func (s *Store) reviewCaseAction(ctx context.Context, guildID, executionID string, decide func(*gorm.DB, *executionRecord, time.Time) error) (*quack.CaseActionExecution, error) {
	now := time.Now().UTC()
	var record executionRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).
			Where("id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", executionID, guildID), &record)
		if err != nil {
			return fmt.Errorf("get case action execution: %w", err)
		}
		if !found {
			return errNotFound
		}
		return decide(tx, &record, now)
	})
	if err != nil || record.ID == "" {
		return nil, notFoundIsNil(err)
	}
	execution := record.model()
	return &execution, nil
}

// saveReview saves a staff review decision with its staff-only timeline event
// and audit entry.
func saveReview(tx *gorm.DB, e *executionRecord, eventType quack.CaseEventType, body, actorID string, audit *quack.AuditLogEntry, now time.Time) error {
	e.UpdatedAt = now
	if err := tx.Save(e).Error; err != nil {
		return fmt.Errorf("save case action execution: %w", err)
	}
	if err := appendCaseEvent(tx, &quack.CaseEvent{
		CaseID:             e.CaseID,
		EventType:          eventType,
		ActorDiscordUserID: actorID,
		ActorType:          "staff",
		Visibility:         quack.EventVisibilityStaff,
		Body:               body,
		MetadataJSON:       jsonObject(map[string]any{"execution_id": e.ID}),
	}, now); err != nil {
		return err
	}
	return writeAudit(tx, audit, e.ID, now)
}

// QueueCaseReversal queues an execution that undoes a succeeded one, such as
// an unban for a ban. The service has already decided the reversal is
// allowed; the store only checks that the original belongs to the case.
// Queuing the same reversal twice returns the first. Reversals are never
// retried automatically, so SafeForRetry is false. It returns nil when the
// original execution is not in the case.
func (s *Store) QueueCaseReversal(ctx context.Context, params quack.QueueCaseReversalParams) (*quack.CaseActionExecution, error) {
	now := time.Now().UTC()
	var reversal executionRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var original executionRecord
		found, err := first(forUpdate(tx).
			Where("id = ? AND case_id = ?", params.OriginalExecutionID, params.CaseID).
			Where("case_id IN (SELECT id FROM cases WHERE guild_id = ?)", params.GuildID), &original)
		if err != nil {
			return fmt.Errorf("get original execution: %w", err)
		}
		if !found {
			return errNotFound
		}
		key := fmt.Sprintf("case:%s:reversal:%s:%s", params.CaseID, original.ID, params.ActionType)
		found, err = first(tx.Where("idempotency_key = ?", key), &reversal)
		if err != nil || found {
			return wrap("get existing reversal", err)
		}
		var lastPosition int
		if err := tx.Model(&executionRecord{}).Where("case_id = ?", params.CaseID).
			Select("COALESCE(MAX(position), -1)").Scan(&lastPosition).Error; err != nil {
			return fmt.Errorf("find reversal position: %w", err)
		}
		originalID := original.ID
		reversal = executionRecord{
			ID:                    quack.NewID(),
			CreatedAt:             now,
			UpdatedAt:             now,
			CaseID:                params.CaseID,
			Position:              lastPosition + 1,
			ActionType:            params.ActionType,
			Status:                quack.ActionExecutionPending,
			IdempotencyKey:        key,
			ConfigSnapshotJSON:    "{}",
			SafeForRetry:          false,
			CorrelationID:         original.CorrelationID,
			ReversalOfExecutionID: &originalID,
			ReversalAppealID:      params.AppealID,
		}
		if err := tx.Create(&reversal).Error; err != nil {
			return fmt.Errorf("queue reversal: %w", err)
		}
		if err := appendCaseEvent(tx, &quack.CaseEvent{
			CaseID:             params.CaseID,
			EventType:          quack.CaseEventReversalQueued,
			ActorDiscordUserID: params.ActorDiscordUserID,
			ActorType:          "staff",
			Visibility:         quack.EventVisibilityStaff,
			Body:               "Action reversal queued",
			MetadataJSON: jsonObject(map[string]any{
				"original_execution_id": original.ID,
				"reversal_execution_id": reversal.ID,
			}),
		}, now); err != nil {
			return err
		}
		return writeAudit(tx, params.Audit, reversal.ID, now)
	})
	if err != nil || reversal.ID == "" {
		return nil, notFoundIsNil(err)
	}
	execution := reversal.model()
	return &execution, nil
}
