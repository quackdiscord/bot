package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/structs"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CreateCaseParams struct {
	Case             structs.Case
	Event            structs.CaseEvent
	ActionExecutions []structs.CaseActionExecution
	Audit            *structs.AuditLogEntry
}

type CreatedCase struct {
	Case             structs.Case
	Event            structs.CaseEvent
	ActionExecutions []structs.CaseActionExecution
}

type CaseHistoryStats struct {
	CaseCount   int64
	WeightTotal int64
}

type ClaimedCaseAction struct {
	Case      structs.Case
	Settings  structs.GuildSettings
	Execution structs.CaseActionExecution
}

type ClaimCaseActionParams struct {
	CaseID   string
	WorkerID string
}

type CompleteCaseActionParams struct {
	ExecutionID         string
	AttemptNumber       uint8
	WorkerID            string
	AttemptStatus       structs.ActionAttemptStatus
	ExecutionStatus     structs.ActionExecutionStatus
	ErrorCode           string
	ErrorMessage        string
	RequestPayloadJSON  string
	ResponsePayloadJSON string
	NextRetryAt         *time.Time
	EventType           structs.CaseEventType
	EventBody           string
	EventMetadataJSON   string
}

type SkipCaseActionsParams struct {
	CaseID        string
	AfterPosition int
	Reason        string
}

func (s *Store) CreateCase(ctx context.Context, params CreateCaseParams) (*CreatedCase, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	caseModel := params.Case
	if err := prepareULIDModel(&caseModel.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare case model: %w", err)
	}
	if caseModel.Status == "" {
		caseModel.Status = structs.CaseStatusOpen
	}
	if caseModel.Source == "" {
		caseModel.Source = structs.CaseSourceAPI
	}
	if caseModel.TemplateSnapshotJSON == "" {
		caseModel.TemplateSnapshotJSON = "{}"
	}
	if caseModel.MetadataJSON == "" {
		caseModel.MetadataJSON = "{}"
	}

	event := params.Event
	actionExecutions := make([]structs.CaseActionExecution, len(params.ActionExecutions))
	copy(actionExecutions, params.ActionExecutions)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		caseNumber, err := nextCaseNumber(tx, caseModel.GuildID)
		if err != nil {
			return err
		}
		caseModel.CaseNumber = caseNumber

		if err := tx.Select("*").Create(&caseModel).Error; err != nil {
			return fmt.Errorf("create case: %w", err)
		}

		event.CaseID = caseModel.ID
		event.GuildID = caseModel.GuildID
		if event.EventType == "" {
			event.EventType = structs.CaseEventCreated
		}
		if event.ActorType == "" {
			event.ActorType = "staff"
		}
		if event.Visibility == "" {
			event.Visibility = structs.EventVisibilityStaff
		}
		if event.MetadataJSON == "" {
			event.MetadataJSON = "{}"
		}
		if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case event model: %w", err)
		}
		if err := tx.Select("*").Create(&event).Error; err != nil {
			return fmt.Errorf("create case event: %w", err)
		}

		for i := range actionExecutions {
			actionExecutions[i].CaseID = caseModel.ID
			if actionExecutions[i].Status == "" {
				actionExecutions[i].Status = structs.ActionExecutionPending
			}
			if actionExecutions[i].ConfigSnapshotJSON == "" {
				actionExecutions[i].ConfigSnapshotJSON = "{}"
			}
			if actionExecutions[i].IdempotencyKey == "" {
				actionExecutions[i].IdempotencyKey = fmt.Sprintf("case:%s:action:%d", caseModel.ID, actionExecutions[i].Position)
			}
			if err := prepareULIDModel(&actionExecutions[i].ULIDModel, now); err != nil {
				return fmt.Errorf("prepare case action execution model: %w", err)
			}
		}
		if len(actionExecutions) > 0 {
			if err := tx.Select("*").Create(&actionExecutions).Error; err != nil {
				return fmt.Errorf("create case action executions: %w", err)
			}
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = caseModel.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &CreatedCase{
		Case:             caseModel,
		Event:            event,
		ActionExecutions: actionExecutions,
	}, nil
}

func (s *Store) CaseHistoryStats(ctx context.Context, guildID, targetDiscordUserID string, scope structs.EscalationScope, since *time.Time) (*CaseHistoryStats, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	query := s.db.WithContext(ctx).Model(&structs.Case{}).Where("guild_id = ?", guildID)
	if scope == structs.EscalationScopeUser {
		query = query.Where("target_discord_user_id = ?", targetDiscordUserID)
	}
	if since != nil {
		query = query.Where("created_at >= ?", *since)
	}

	var row struct {
		CaseCount   int64
		WeightTotal int64
	}
	if err := query.Select("COUNT(*) AS case_count, COALESCE(SUM(weight), 0) AS weight_total").Scan(&row).Error; err != nil {
		return nil, fmt.Errorf("case history stats: %w", err)
	}

	return &CaseHistoryStats{CaseCount: row.CaseCount, WeightTotal: row.WeightTotal}, nil
}

func (s *Store) ListCases(ctx context.Context, guildID string) ([]structs.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var cases []structs.Case
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Order("case_number ASC").Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}

	return cases, nil
}

func (s *Store) ListCaseEvents(ctx context.Context, caseID string) ([]structs.CaseEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var events []structs.CaseEvent
	if err := s.db.WithContext(ctx).Where("case_id = ?", caseID).Order("created_at ASC").Find(&events).Error; err != nil {
		return nil, fmt.Errorf("list case events: %w", err)
	}

	return events, nil
}

func (s *Store) ListCaseActionExecutions(ctx context.Context, caseID string) ([]structs.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var executions []structs.CaseActionExecution
	if err := s.db.WithContext(ctx).Where("case_id = ?", caseID).Order("position ASC").Find(&executions).Error; err != nil {
		return nil, fmt.Errorf("list case action executions: %w", err)
	}

	return executions, nil
}

func nextCaseNumber(tx *gorm.DB, guildID string) (uint64, error) {
	var latest structs.Case
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("guild_id = ?", guildID).
		Order("case_number DESC").
		Limit(1).
		Find(&latest).Error
	if err != nil {
		return 0, fmt.Errorf("get latest case number: %w", err)
	}
	if latest.CaseNumber == 0 {
		return 1, nil
	}
	return latest.CaseNumber + 1, nil
}

func (s *Store) ClaimNextCaseAction(ctx context.Context, params ClaimCaseActionParams) (*ClaimedCaseAction, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var claimed *ClaimedCaseAction
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var caseModel structs.Case
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
		if err := tx.Model(&structs.CaseActionExecution{}).
			Where("case_id = ? AND status = ?", params.CaseID, structs.ActionExecutionRunning).
			Count(&running).Error; err != nil {
			return fmt.Errorf("count running case actions: %w", err)
		}
		if running > 0 {
			return nil
		}

		var execution structs.CaseActionExecution
		result = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("case_id = ? AND status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)",
				params.CaseID,
				[]structs.ActionExecutionStatus{structs.ActionExecutionPending, structs.ActionExecutionRetrying},
				now,
			).
			Order("position ASC").
			Limit(1).
			Find(&execution)
		if result.Error != nil {
			return fmt.Errorf("claim case action execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return nil
		}

		execution.Status = structs.ActionExecutionRunning
		execution.AttemptCount++
		execution.StartedAt = &now
		execution.FinishedAt = nil
		execution.NextRetryAt = nil
		execution.UpdatedAt = now
		if err := tx.Select("*").Save(&execution).Error; err != nil {
			return fmt.Errorf("mark case action running: %w", err)
		}

		if caseModel.Status == structs.CaseStatusOpen {
			caseModel.Status = structs.CaseStatusActionRunning
			caseModel.UpdatedAt = now
			if err := tx.Save(&caseModel).Error; err != nil {
				return fmt.Errorf("mark case action running: %w", err)
			}
		}

		var settings structs.GuildSettings
		result = tx.Where("guild_id = ?", caseModel.GuildID).Limit(1).Find(&settings)
		if result.Error != nil {
			return fmt.Errorf("get guild settings for action: %w", result.Error)
		}

		claimed = &ClaimedCaseAction{
			Case:      caseModel,
			Settings:  settings,
			Execution: execution,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

func (s *Store) CompleteCaseAction(ctx context.Context, params CompleteCaseActionParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var execution structs.CaseActionExecution
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", params.ExecutionID).
			Limit(1).
			Find(&execution)
		if result.Error != nil {
			return fmt.Errorf("get case action execution: %w", result.Error)
		}
		if result.RowsAffected == 0 {
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
		attempt := structs.CaseActionAttempt{
			ExecutionID:         execution.ID,
			AttemptNumber:       params.AttemptNumber,
			Status:              params.AttemptStatus,
			WorkerID:            params.WorkerID,
			StartedAt:           startedAt,
			FinishedAt:          &now,
			DurationMS:          now.Sub(startedAt).Milliseconds(),
			ErrorCode:           params.ErrorCode,
			ErrorMessage:        params.ErrorMessage,
			RequestPayloadJSON:  params.RequestPayloadJSON,
			ResponsePayloadJSON: params.ResponsePayloadJSON,
		}
		if attempt.AttemptNumber == 0 {
			attempt.AttemptNumber = execution.AttemptCount
		}
		if err := prepareULIDModel(&attempt.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case action attempt model: %w", err)
		}
		if err := tx.Select("*").Create(&attempt).Error; err != nil {
			return fmt.Errorf("create case action attempt: %w", err)
		}

		execution.Status = params.ExecutionStatus
		execution.LastErrorCode = params.ErrorCode
		execution.LastError = params.ErrorMessage
		execution.FinishedAt = &now
		execution.NextRetryAt = params.NextRetryAt
		execution.UpdatedAt = now
		if params.ExecutionStatus == structs.ActionExecutionRetrying {
			execution.FinishedAt = nil
		}
		if err := tx.Select("*").Save(&execution).Error; err != nil {
			return fmt.Errorf("update case action execution: %w", err)
		}

		if params.EventType != "" {
			event := structs.CaseEvent{
				CaseID:       execution.CaseID,
				EventType:    params.EventType,
				ActorType:    "system",
				Visibility:   structs.EventVisibilityStaff,
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

		return updateCaseStatusFromActions(tx, execution.CaseID, now)
	})
}

func (s *Store) SkipCaseActions(ctx context.Context, params SkipCaseActionsParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var executions []structs.CaseActionExecution
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("case_id = ? AND position > ? AND status IN ?", params.CaseID, params.AfterPosition, []structs.ActionExecutionStatus{structs.ActionExecutionPending, structs.ActionExecutionRetrying}).
			Order("position ASC").
			Find(&executions).Error; err != nil {
			return fmt.Errorf("list case actions to skip: %w", err)
		}

		for i := range executions {
			executions[i].Status = structs.ActionExecutionSkipped
			executions[i].LastErrorCode = "blocked_by_previous_action"
			executions[i].LastError = params.Reason
			executions[i].FinishedAt = &now
			executions[i].NextRetryAt = nil
			executions[i].UpdatedAt = now
			if err := tx.Select("*").Save(&executions[i]).Error; err != nil {
				return fmt.Errorf("skip case action execution: %w", err)
			}
		}

		return updateCaseStatusFromActions(tx, params.CaseID, now)
	})
}

func (s *Store) ListExecutableCaseIDs(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if limit <= 0 {
		limit = 100
	}

	now := time.Now().UTC()
	var rows []struct {
		CaseID string
	}
	if err := s.db.WithContext(ctx).
		Model(&structs.CaseActionExecution{}).
		Select("case_id").
		Where("status IN ? AND (next_retry_at IS NULL OR next_retry_at <= ?)", []structs.ActionExecutionStatus{structs.ActionExecutionPending, structs.ActionExecutionRetrying}, now).
		Group("case_id").
		Order("MIN(position) ASC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list executable case ids: %w", err)
	}

	caseIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		caseIDs = append(caseIDs, row.CaseID)
	}
	return caseIDs, nil
}

func appendCaseEvent(tx *gorm.DB, event *structs.CaseEvent, now time.Time) error {
	var caseModel structs.Case
	result := tx.Where("id = ?", event.CaseID).Limit(1).Find(&caseModel)
	if result.Error != nil {
		return fmt.Errorf("get case for event: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil
	}

	event.GuildID = caseModel.GuildID
	if event.Visibility == "" {
		event.Visibility = structs.EventVisibilityStaff
	}
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
		return fmt.Errorf("prepare case event model: %w", err)
	}
	if err := tx.Select("*").Create(event).Error; err != nil {
		return fmt.Errorf("create case event: %w", err)
	}
	return nil
}

func updateCaseStatusFromActions(tx *gorm.DB, caseID string, now time.Time) error {
	var executions []structs.CaseActionExecution
	if err := tx.Where("case_id = ?", caseID).Find(&executions).Error; err != nil {
		return fmt.Errorf("list case actions for status: %w", err)
	}
	if len(executions) == 0 {
		return nil
	}

	nextStatus := structs.CaseStatusCompleted
	for _, execution := range executions {
		switch execution.Status {
		case structs.ActionExecutionFailed:
			nextStatus = structs.CaseStatusFailed
		case structs.ActionExecutionPending, structs.ActionExecutionRunning, structs.ActionExecutionRetrying:
			if nextStatus != structs.CaseStatusFailed {
				nextStatus = structs.CaseStatusActionRunning
			}
		}
	}

	updates := map[string]any{
		"status":     nextStatus,
		"updated_at": now,
	}
	if nextStatus == structs.CaseStatusCompleted || nextStatus == structs.CaseStatusFailed {
		updates["resolved_at"] = now
		updates["resolved_by_discord_user_id"] = "system"
	}

	if err := tx.Model(&structs.Case{}).Where("id = ?", caseID).Updates(updates).Error; err != nil {
		return fmt.Errorf("update case status: %w", err)
	}
	return nil
}
