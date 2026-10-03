package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimNextCaseAction atomically claims next case action so concurrent workers cannot execute it twice.
func (s *Store) ClaimNextCaseAction(ctx context.Context, params quack.ClaimCaseActionParams) (*quack.ClaimedCaseAction, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var claimed *quack.ClaimedCaseAction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var caseModel quack.Case
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.CaseID).
			Limit(1).
			Find(&caseModel)
		if result.Error != nil {
			return fmt.Errorf("get case for action claim: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}

		var running int64
		if err := tx.Model(&quack.CaseActionExecution{}).
			Where("case_id = ? AND status = ? AND lease_expires_at > ?", params.CaseID, quack.ActionExecutionRunning, now).
			Count(&running).Error; err != nil {
			return fmt.Errorf("count running case actions: %w", err)
		}
		if running > 0 {
			return nil
		}

		var execution quack.CaseActionExecution
		result = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("case_id = ? AND ((status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)) OR (status = ? AND lease_expires_at <= ?))", params.CaseID, []quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRetrying}, now, quack.ActionExecutionRunning, now).
			Order("position ASC").
			Limit(1).
			Find(&execution)
		if result.Error != nil {
			return fmt.Errorf("claim case action execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}

		recovering := execution.Status == quack.ActionExecutionRunning
		if execution.AttemptCount > 0 {
			var prior quack.CaseActionAttempt
			priorResult := tx.Where("execution_id = ? AND attempt_number = ? AND status = ?", execution.ID, execution.AttemptCount, quack.ActionAttemptRunning).First(&prior)
			if priorResult.Error == nil {
				prior.Status = quack.ActionAttemptFailed
				prior.FinishedAt = &now
				prior.DurationMS = now.Sub(prior.StartedAt).Milliseconds()
				prior.ErrorCode = "lease_expired"
				prior.ErrorMessage = "worker lease expired before completion"
				prior.UpdatedAt = now
				if err := tx.Select("*").Save(&prior).Error; err != nil {
					return fmt.Errorf("close expired action attempt: %w", err)
				}
				if err := createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: caseModel.GuildID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionActionRecovered), ResourceType: "case_action_execution", ResourceID: execution.ID, Result: quack.AuditResultFailure, FailureReason: "lease_expired", CorrelationID: firstNonEmpty(execution.CorrelationID, caseModel.CorrelationID), MetadataJSON: marshalJSONObject(map[string]any{"case_id": caseModel.ID, "attempt_number": prior.AttemptNumber, "recovery": "lease_reclaimed"})}, now); err != nil {
					return err
				}
			} else if !errors.Is(priorResult.Error, gorm.ErrRecordNotFound) {
				return priorResult.Error
			}
		}
		if recovering {
			// An expired lease means a worker stopped mid-attempt, so Discord
			// may or may not have applied the action. Never repeat it blindly:
			// fail it for staff review.
			return failExpiredAction(tx, caseModel, &execution, now)
		}
		execution.Status = quack.ActionExecutionRunning
		execution.AttemptCount++
		execution.StartedAt = &now
		execution.FinishedAt = nil
		execution.NextRetryAt = nil
		leaseToken := quack.NewID()
		leaseExpiry := now.Add(2 * time.Minute)
		execution.LeaseToken = leaseToken
		execution.LeaseExpiresAt = &leaseExpiry
		execution.UpdatedAt = now
		if err := tx.Select("*").Save(&execution).Error; err != nil {
			return fmt.Errorf("mark case action running: %w", err)
		}
		attempt := quack.CaseActionAttempt{ExecutionID: execution.ID, AttemptNumber: execution.AttemptCount, Status: quack.ActionAttemptRunning, WorkerID: params.WorkerID, StartedAt: now, RequestPayloadJSON: "{}", ResponsePayloadJSON: "{}"}
		if err := prepareULIDModel(&attempt.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Select("*").Create(&attempt).Error; err != nil {
			return fmt.Errorf("create running action attempt: %w", err)
		}
		if err := createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: caseModel.GuildID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionActionAttempt), ResourceType: "case_action_execution", ResourceID: execution.ID, Result: quack.AuditResultSuccess, CorrelationID: firstNonEmpty(execution.CorrelationID, caseModel.CorrelationID), MetadataJSON: marshalJSONObject(map[string]any{"case_id": caseModel.ID, "attempt_number": attempt.AttemptNumber, "status": attempt.Status})}, now); err != nil {
			return err
		}

		claimed = &quack.ClaimedCaseAction{
			Case:      caseModel,
			Execution: execution,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

// failExpiredAction fences the old worker and exposes an uncertain outcome in
// the existing failure queue without issuing a second Discord request.
func failExpiredAction(tx *gorm.DB, item quack.Case, execution *quack.CaseActionExecution, now time.Time) error {
	execution.Status = quack.ActionExecutionFailed
	execution.LastErrorCode = "lease_expired_review_required"
	execution.LastError = "Worker stopped before recording the result; confirm the Discord outcome before retrying"
	execution.FinishedAt = &now
	execution.UpdatedAt = now
	execution.LeaseToken = ""
	execution.LeaseExpiresAt = nil
	execution.NextRetryAt = nil
	if err := tx.Select("*").Save(execution).Error; err != nil {
		return fmt.Errorf("record expired action for review: %w", err)
	}
	if err := appendCaseEvent(tx, &quack.CaseEvent{CaseID: item.ID, EventType: quack.CaseEventActionFailed,
		ActorType: "system", Visibility: quack.EventVisibilityPublic,
		Body:         "Discord enforcement could not be confirmed and requires staff review",
		MetadataJSON: marshalJSONObject(map[string]any{"execution_id": execution.ID})}, now); err != nil {
		return err
	}
	return createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: item.GuildID,
		Source: quack.AuditSourceSystem, Action: string(quack.AuditActionActionRecovered),
		ResourceType: "case_action_execution", ResourceID: execution.ID,
		Result: quack.AuditResultFailure, FailureReason: execution.LastErrorCode,
		CorrelationID: firstNonEmpty(execution.CorrelationID, item.CorrelationID),
		MetadataJSON:  marshalJSONObject(map[string]any{"case_id": item.ID, "recovery": "staff_review_required"})}, now)
}
