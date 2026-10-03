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

// CompleteCaseAction persists the terminal or retry outcome for case action.
func (s *Store) CompleteCaseAction(ctx context.Context, params quack.CompleteCaseActionParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var execution quack.CaseActionExecution
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", params.ExecutionID)
		if params.LeaseToken != "" {
			query = query.Where("lease_token = ?", params.LeaseToken)
		}
		result := query.
			Limit(1).
			Find(&execution)
		if result.Error != nil {
			return fmt.Errorf("get case action execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			if params.LeaseToken != "" {
				return errors.New("case action lease is stale")
			}
			return nil
		}

		if params.RequestPayloadJSON == "" {
			params.RequestPayloadJSON = "{}"
		}
		if params.ResponsePayloadJSON == "" {
			params.ResponsePayloadJSON = "{}"
		}

		startedAt := now
		if execution.StartedAt != nil {
			startedAt = *execution.StartedAt
		}
		attemptNumber := params.AttemptNumber
		if attemptNumber == 0 {
			attemptNumber = execution.AttemptCount
		}
		var attempt quack.CaseActionAttempt
		attemptResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("execution_id = ? AND attempt_number = ?", execution.ID, attemptNumber).First(&attempt)
		if errors.Is(attemptResult.Error, gorm.ErrRecordNotFound) {
			attempt = quack.CaseActionAttempt{ExecutionID: execution.ID, AttemptNumber: attemptNumber, StartedAt: startedAt, WorkerID: params.WorkerID}
			if err := prepareULIDModel(&attempt.ULIDModel, now); err != nil {
				return err
			}
		} else if attemptResult.Error != nil {
			return attemptResult.Error
		}
		attempt.Status = params.AttemptStatus
		attempt.FinishedAt = &now
		attempt.DurationMS = now.Sub(attempt.StartedAt).Milliseconds()
		attempt.ErrorCode = params.ErrorCode
		attempt.ErrorMessage = params.ErrorMessage
		attempt.RequestPayloadJSON = params.RequestPayloadJSON
		attempt.ResponsePayloadJSON = params.ResponsePayloadJSON
		attempt.UpdatedAt = now
		if err := tx.Select("*").Save(&attempt).Error; err != nil {
			return fmt.Errorf("complete case action attempt: %w", err)
		}

		execution.Status = params.ExecutionStatus
		execution.LastErrorCode = params.ErrorCode
		execution.LastError = params.ErrorMessage
		execution.FinishedAt = &now
		execution.NextRetryAt = params.NextRetryAt
		execution.LeaseToken = ""
		execution.LeaseExpiresAt = nil
		execution.UpdatedAt = now
		if params.ExecutionStatus == quack.ActionExecutionRetrying {
			execution.FinishedAt = nil
		}
		if err := tx.Select("*").Save(&execution).Error; err != nil {
			return fmt.Errorf("update case action execution: %w", err)
		}

		if params.EventType != "" {
			event := quack.CaseEvent{
				CaseID:       execution.CaseID,
				EventType:    params.EventType,
				ActorType:    "system",
				Visibility:   quack.EventVisibilityPublic,
				Body:         params.EventBody,
				MetadataJSON: params.EventMetadataJSON,
			}
			if event.MetadataJSON == "" {
				event.MetadataJSON = "{}"
			}
			if err := appendCaseEvent(tx, &event, now); err != nil {
				return err
			}
		}

		if err := createCaseActionAudit(tx, execution, params, now); err != nil {
			return err
		}

		return nil
	})
}

// createCaseActionAudit creates case action audit while preserving validation, authorization, and persistence invariants.
func createCaseActionAudit(tx *gorm.DB, execution quack.CaseActionExecution, params quack.CompleteCaseActionParams, now time.Time) error {
	var caseModel quack.Case
	result := tx.Where("id = ?", execution.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for action audit: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	action := "case_action.succeeded"
	resultValue := quack.AuditResultSuccess
	switch params.ExecutionStatus {
	case quack.ActionExecutionRetrying:
		action = "case_action.retrying"
		resultValue = quack.AuditResultFailure
	case quack.ActionExecutionFailed:
		action = "case_action.failed"
		resultValue = quack.AuditResultFailure
	}

	return createAuditLogEntry(tx, &quack.AuditLogEntry{
		GuildID:       caseModel.GuildID,
		Source:        quack.AuditSourceSystem,
		Action:        action,
		ResourceType:  "case_action_execution",
		ResourceID:    execution.ID,
		Result:        resultValue,
		FailureReason: params.ErrorMessage,
		CorrelationID: firstNonEmpty(params.CorrelationID, execution.CorrelationID, caseModel.CorrelationID),
		RequestID:     params.RequestID,
		MetadataJSON: marshalJSONObject(map[string]any{
			"case_id":        caseModel.ID,
			"case_number":    caseModel.CaseNumber,
			"action_type":    execution.ActionType,
			"attempt_number": params.AttemptNumber,
			"retrying":       params.ExecutionStatus == quack.ActionExecutionRetrying,
		}),
	}, now)
}
