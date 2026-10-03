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

// ListFailedCaseActions returns the active staff-review queue with stable newest-first ordering.
func (s *Store) ListFailedCaseActions(ctx context.Context, filter quack.FailedCaseActionFilter) (*quack.FailedCaseActionResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := s.db.WithContext(ctx).Model(&quack.CaseActionExecution{}).Where("status = ? AND dismissed_at IS NULL AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", quack.ActionExecutionFailed, filter.GuildID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var items []quack.CaseActionExecution
	if err := query.Order("updated_at DESC, id DESC").Limit(limit).Offset(offset).Find(&items).Error; err != nil {
		return nil, err
	}
	return &quack.FailedCaseActionResult{Executions: items, Total: total}, nil
}

// RetryCaseAction requeues the same immutable action after live authorization has been performed by the service.
func (s *Store) RetryCaseAction(ctx context.Context, params quack.RetryCaseActionParams) (*quack.CaseActionExecution, error) {
	return s.controlCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, item *quack.CaseActionExecution, now time.Time) error {
		if item.Status == quack.ActionExecutionPending || item.Status == quack.ActionExecutionRetrying {
			return nil
		}
		if item.Status != quack.ActionExecutionFailed {
			return errors.New("action is not failed")
		}
		item.Status = quack.ActionExecutionPending
		item.NextRetryAt = nil
		item.StartedAt = nil
		item.FinishedAt = nil
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		item.DismissedAt = nil
		item.DismissedByDiscordUserID = ""
		item.LastErrorCode = ""
		item.LastError = ""
		if err := tx.Select("*").Save(item).Error; err != nil {
			return err
		}
		event := quack.CaseEvent{CaseID: item.CaseID, EventType: quack.CaseEventActionRetried, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: quack.EventVisibilityStaff, Body: "Action retry requested", MetadataJSON: marshalJSONObject(map[string]any{"execution_id": item.ID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return createOptionalActionControlAudit(tx, params.Audit, item.ID, now)
	}, params.Audit)
}

// DismissCaseAction removes a failed action from the active queue without deleting its attempts.
func (s *Store) DismissCaseAction(ctx context.Context, params quack.DismissCaseActionParams) (*quack.CaseActionExecution, error) {
	return s.controlCaseAction(ctx, params.GuildID, params.ExecutionID, func(tx *gorm.DB, item *quack.CaseActionExecution, now time.Time) error {
		if item.DismissedAt != nil {
			return nil
		}
		if item.Status != quack.ActionExecutionFailed {
			return errors.New("action is not failed")
		}
		item.DismissedAt = &now
		item.DismissedByDiscordUserID = params.ActorDiscordUserID
		if err := tx.Select("*").Save(item).Error; err != nil {
			return err
		}
		event := quack.CaseEvent{CaseID: item.CaseID, EventType: quack.CaseEventActionDismissed, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: quack.EventVisibilityStaff, Body: "Action failure dismissed", MetadataJSON: marshalJSONObject(map[string]any{"execution_id": item.ID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return createOptionalActionControlAudit(tx, params.Audit, item.ID, now)
	}, params.Audit)
}

// controlCaseAction serializes one guild-scoped action control mutation.
func (s *Store) controlCaseAction(ctx context.Context, guildID, executionID string, mutate func(*gorm.DB, *quack.CaseActionExecution, time.Time) error, audit *quack.AuditLogEntry) (*quack.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var item quack.CaseActionExecution
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", executionID, guildID).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		return mutate(tx, &item, now)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &item, nil
}

// createOptionalActionControlAudit appends caller-provided audit evidence inside the control transaction.
func createOptionalActionControlAudit(tx *gorm.DB, audit *quack.AuditLogEntry, resourceID string, now time.Time) error {
	if audit == nil {
		return nil
	}
	entry := *audit
	entry.ResourceID = resourceID
	return createAuditLogEntry(tx, &entry, now)
}

// QueueCaseReversal appends a reversal execution for a succeeded action.
// The service has already decided the reversal is allowed; this only checks
// that the original execution belongs to the case.
func (s *Store) QueueCaseReversal(ctx context.Context, params quack.QueueCaseReversalParams) (*quack.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var reversal quack.CaseActionExecution
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var original quack.CaseActionExecution
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND case_id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", params.OriginalExecutionID, params.CaseID, params.GuildID).First(&original)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return result.Error
		}
		var maxPosition int
		if err := tx.Model(&quack.CaseActionExecution{}).Where("case_id = ?", params.CaseID).Select("COALESCE(MAX(position), -1)").Scan(&maxPosition).Error; err != nil {
			return fmt.Errorf("find reversal position: %w", err)
		}
		originalID := original.ID
		reversal = quack.CaseActionExecution{CaseID: params.CaseID, Position: maxPosition + 1, ActionType: params.ActionType, Status: quack.ActionExecutionPending, IdempotencyKey: fmt.Sprintf("case:%s:reversal:%s:%s", params.CaseID, original.ID, params.ActionType), ConfigSnapshotJSON: "{}", SafeForRetry: false, ReversalOfExecutionID: &originalID, ReversalAppealID: params.AppealID}
		var existing quack.CaseActionExecution
		existingResult := tx.Where("idempotency_key = ?", reversal.IdempotencyKey).First(&existing)
		if existingResult.Error == nil {
			reversal = existing
			return nil
		} else if !errors.Is(existingResult.Error, gorm.ErrRecordNotFound) {
			return existingResult.Error
		}
		if err := prepareULIDModel(&reversal.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Select("*").Create(&reversal).Error; err != nil {
			return fmt.Errorf("queue reversal: %w", err)
		}
		event := quack.CaseEvent{CaseID: params.CaseID, EventType: quack.CaseEventReversalQueued, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: quack.EventVisibilityStaff, Body: "Action reversal queued", MetadataJSON: marshalJSONObject(map[string]any{"original_execution_id": original.ID, "reversal_execution_id": reversal.ID})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return createOptionalActionControlAudit(tx, params.Audit, reversal.ID, now)
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &reversal, nil
}

// PrepareCaseNotification durably records a DM channel opened before kick or ban enforcement.
func (s *Store) PrepareCaseNotification(ctx context.Context, caseID, channelID, errorMessage string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	updates := map[string]any{"prepared_channel_discord_id": channelID, "updated_at": time.Now().UTC()}
	if channelID != "" {
		updates["status"] = quack.NotificationPrepared
	} else {
		updates["last_error_code"] = "dm_prepare_failed"
		updates["last_error"] = errorMessage
	}
	return s.db.WithContext(ctx).Model(&quack.CaseNotification{}).Where("case_id = ? AND status = ?", caseID, quack.NotificationPending).Updates(updates).Error
}

// ClaimCaseNotification claims at most one delivery after enforcement reaches a terminal outcome.
func (s *Store) ClaimCaseNotification(ctx context.Context, params quack.ClaimCaseNotificationParams) (*quack.CaseNotification, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var claimed *quack.CaseNotification
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&quack.CaseActionExecution{}).Where("case_id = ? AND status IN ?", params.CaseID, []quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRunning, quack.ActionExecutionRetrying}).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return nil
		}
		var item quack.CaseNotification
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("case_id = ? AND (status IN ? OR (status = ? AND lease_expires_at <= ?))", params.CaseID, []quack.NotificationStatus{quack.NotificationPending, quack.NotificationPrepared}, quack.NotificationClaimed, now).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil
		}
		if result.Error != nil {
			return result.Error
		}
		token := quack.NewID()
		expires := now.Add(2 * time.Minute)
		item.Status = quack.NotificationClaimed
		item.AttemptCount++
		item.LeaseToken = token
		item.LeaseExpiresAt = &expires
		item.UpdatedAt = now
		if err := tx.Select("*").Save(&item).Error; err != nil {
			return err
		}
		claimed = &item
		return nil
	})
	return claimed, err
}

// BeginCaseNotificationDelivery crosses the final durable fence immediately before the external send.
func (s *Store) BeginCaseNotificationDelivery(ctx context.Context, notificationID, leaseToken string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	result := s.db.WithContext(ctx).Model(&quack.CaseNotification{}).Where("id = ? AND lease_token = ? AND status = ?", notificationID, leaseToken, quack.NotificationClaimed).Updates(map[string]any{"status": quack.NotificationSending, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("case notification lease is stale")
	}
	return nil
}

// CompleteCaseNotification applies a fenced terminal result; stale workers cannot overwrite a later decision.
func (s *Store) CompleteCaseNotification(ctx context.Context, params quack.CompleteCaseNotificationParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item quack.CaseNotification
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND lease_token = ? AND status = ?", params.NotificationID, params.LeaseToken, quack.NotificationSending).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return errors.New("case notification lease is stale")
		}
		if result.Error != nil {
			return result.Error
		}
		item.Status = params.Status
		item.PreparedChannelDiscordID = params.PreparedChannelDiscordID
		item.RenderedMessage = params.RenderedMessage
		item.DeliveryMessageDiscordID = params.DeliveryMessageDiscordID
		item.LastErrorCode = params.ErrorCode
		item.LastError = params.ErrorMessage
		item.LeaseToken = ""
		item.LeaseExpiresAt = nil
		item.UpdatedAt = now
		if params.Status == quack.NotificationSent {
			item.SentAt = &now
		}
		if err := tx.Select("*").Save(&item).Error; err != nil {
			return err
		}
		event := quack.CaseEvent{CaseID: item.CaseID, EventType: params.EventType, ActorType: "system", Visibility: quack.EventVisibilityPublic, Body: "Member notification delivery updated", MetadataJSON: marshalJSONObject(map[string]any{"status": params.Status})}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		var caseModel quack.Case
		if err := tx.Where("id = ?", item.CaseID).First(&caseModel).Error; err != nil {
			return err
		}
		auditResult := quack.AuditResultSuccess
		action := "case_notification.sent"
		if params.Status != quack.NotificationSent {
			auditResult = quack.AuditResultFailure
			action = "case_notification.failed"
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: caseModel.GuildID, Source: quack.AuditSourceSystem, Action: action, ResourceType: "case_notification", ResourceID: item.ID, Result: auditResult, FailureReason: params.ErrorMessage, CorrelationID: caseModel.CorrelationID, MetadataJSON: marshalJSONObject(map[string]any{"case_id": caseModel.ID, "status": params.Status})}, now)
	})
}
