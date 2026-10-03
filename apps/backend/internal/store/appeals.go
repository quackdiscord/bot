package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetGuildAppealSettings returns a guild's configured future appeal form, or nil to select the product default.
func (s *Store) GetGuildAppealSettings(ctx context.Context, guildID string) (*quack.GuildAppealSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record GuildAppealSettingsRecord
	result := s.db.WithContext(ctx).Where("guild_id = ?", guildID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return guildAppealSettingsModel(record), nil
}

// UpdateGuildAppealSettings replaces only the form used by future submissions and appends its audit record atomically.
func (s *Store) UpdateGuildAppealSettings(ctx context.Context, params quack.UpdateGuildAppealSettingsParams) (*quack.GuildAppealSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	settings := params.Settings
	if err := prepareULIDModel(&settings.ULIDModel, now); err != nil {
		return nil, err
	}
	record := guildAppealSettingsRecord(settings)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing GuildAppealSettingsRecord
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", settings.GuildID).First(&existing)
		if result.Error == nil {
			record.ID = existing.ID
			record.CreatedAt = existing.CreatedAt
			record.UpdatedAt = now
			if err := tx.Select("questions_json", "updated_by_discord_user_id", "updated_at").Updates(&record).Error; err != nil {
				return err
			}
		} else if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			if err := tx.Create(&record).Error; err != nil {
				return err
			}
		} else {
			return result.Error
		}
		audit := params.Audit
		audit.ResourceID = record.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return guildAppealSettingsModel(record), nil
}

// CreateAppeal inserts one case-unique appeal with its first immutable event, public case history, audit, and staff notification.
func (s *Store) CreateAppeal(ctx context.Context, params quack.CreateAppealParams) (*quack.Appeal, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	appeal := params.Appeal
	if err := prepareULIDModel(&appeal.ULIDModel, now); err != nil {
		return nil, err
	}
	event := params.Event
	event.AppealID = appeal.ID
	event.GuildID = appeal.GuildID
	if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
		return nil, err
	}
	notification := params.Notification
	notification.AppealID = appeal.ID
	notification.EventID = event.ID
	notification.GuildID = appeal.GuildID
	if err := prepareULIDModel(&notification.ULIDModel, now); err != nil {
		return nil, err
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if appeal.CaseID == nil || strings.TrimSpace(*appeal.CaseID) == "" {
			return quack.ErrAppealCaseIneligible
		}
		var item CaseRecord
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", *appeal.CaseID, appeal.GuildID).First(&item)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return quack.ErrAppealCaseIneligible
		}
		if result.Error != nil {
			return result.Error
		}
		// The service decided the case is appealable. Re-check only what can
		// change concurrently: the case may have been voided since.
		if item.TargetDiscordUserID != appeal.TargetDiscordUserID || item.Validity != quack.CaseValidityValid {
			return quack.ErrAppealCaseIneligible
		}
		var existing int64
		if err := tx.Model(&appealV5Record{}).Where("case_id = ?", item.ID).Count(&existing).Error; err != nil {
			return err
		}
		if existing != 0 {
			return quack.ErrAppealAlreadyExists
		}
		if err := tx.Create(appealRecord(appeal)).Error; err != nil {
			if isDuplicateError(err) {
				return quack.ErrAppealAlreadyExists
			}
			return err
		}
		if err := tx.Create(appealEventRecord(event)).Error; err != nil {
			return err
		}
		caseEvent := params.CaseEvent
		caseEvent.CaseID = item.ID
		caseEvent.GuildID = item.GuildID
		if err := appendCaseEvent(tx, &caseEvent, now); err != nil {
			return err
		}
		if err := tx.Create(appealNotificationRecord(notification)).Error; err != nil {
			return err
		}
		audit := params.Audit
		audit.ResourceID = appeal.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

// GetAppealByID returns one appeal without applying caller authorization.
func (s *Store) GetAppealByID(ctx context.Context, appealID string) (*quack.Appeal, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record appealV5Record
	result := s.db.WithContext(ctx).Where("id = ?", appealID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return appealModel(record), nil
}

// GetAppealByCaseID returns the only appeal for a case, if any.
func (s *Store) GetAppealByCaseID(ctx context.Context, caseID string) (*quack.Appeal, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record appealV5Record
	result := s.db.WithContext(ctx).Where("case_id = ?", caseID).First(&record)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return appealModel(record), nil
}

// ListAppeals returns stable newest-first staff queue pagination for one guild.
func (s *Store) ListAppeals(ctx context.Context, params quack.AppealListParams) (*quack.AppealListResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	query := s.db.WithContext(ctx).Model(&appealV5Record{}).Where("guild_id = ?", params.GuildID)
	if params.Status != "" {
		query = query.Where("status = ?", params.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	var records []appealV5Record
	if err := query.Order("created_at DESC, id DESC").Limit(params.Limit).Offset(params.Offset).Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]quack.Appeal, 0, len(records))
	for _, record := range records {
		items = append(items, *appealModel(record))
	}
	return &quack.AppealListResult{Appeals: items, Total: total}, nil
}

// ListAppealEvents returns one immutable timeline in creation order.
func (s *Store) ListAppealEvents(ctx context.Context, appealID string) ([]quack.AppealEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var records []appealEventV5Record
	if err := s.db.WithContext(ctx).Where("appeal_id = ?", appealID).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, err
	}
	items := make([]quack.AppealEvent, 0, len(records))
	for _, record := range records {
		items = append(items, appealEventModel(record))
	}
	return items, nil
}

// AppendAppealInformation appends a member response and reopens the same appeal for staff review.
func (s *Store) AppendAppealInformation(ctx context.Context, params quack.AppendAppealInformationParams) (*quack.Appeal, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var updated appealV5Record
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", params.AppealID).First(&updated)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) || updated.TargetDiscordUserID != params.TargetDiscordUserID {
			return quack.ErrAppealStateConflict
		}
		if result.Error != nil {
			return result.Error
		}
		if updated.Status != quack.AppealStatusNeedsInformation {
			return quack.ErrAppealStateConflict
		}
		updated.Status = quack.AppealStatusPending
		updated.Version++
		updated.UpdatedAt = now
		result = tx.Model(&appealV5Record{}).Where("id = ? AND version = ?", updated.ID, updated.Version-1).Updates(map[string]any{"status": updated.Status, "version": updated.Version, "updated_at": now})
		if result.Error != nil || result.RowsAffected != 1 {
			return quack.ErrAppealStateConflict
		}
		event := params.Event
		event.AppealID = updated.ID
		event.GuildID = updated.GuildID
		event.Body = params.Body
		if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealEventRecord(event)).Error; err != nil {
			return err
		}
		notification := params.Notification
		notification.AppealID = updated.ID
		notification.EventID = event.ID
		notification.GuildID = updated.GuildID
		if err := prepareULIDModel(&notification.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealNotificationRecord(notification)).Error; err != nil {
			return err
		}
		audit := params.Audit
		audit.ResourceID = updated.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return appealModel(updated), nil
}

// TransitionAppeal applies one staff timeline transition and optionally voids the case in the same transaction.
func (s *Store) TransitionAppeal(ctx context.Context, params quack.TransitionAppealParams) (*quack.Appeal, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var updated appealV5Record
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", params.AppealID, params.GuildID).First(&updated)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return quack.ErrAppealStateConflict
		}
		if result.Error != nil {
			return result.Error
		}
		if !appealStatusIn(updated.Status, params.AllowedFrom) {
			return quack.ErrAppealStateConflict
		}
		if params.VoidCase {
			if updated.CaseID == nil {
				return quack.ErrAppealCaseIneligible
			}
			var item CaseRecord
			caseResult := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND guild_id = ?", *updated.CaseID, params.GuildID).First(&item)
			if caseResult.Error != nil || item.Validity != quack.CaseValidityValid {
				return quack.ErrAppealStateConflict
			}
			caseUpdates := map[string]any{"status": quack.CaseValidityVoided, "voided_reason": "Appeal accepted", "voided_by_discord_user_id": params.ActorDiscordUserID, "voided_at": now, "updated_at": now}
			if result := tx.Model(&CaseRecord{}).Where("id = ? AND status = ?", item.ID, quack.CaseValidityValid).Updates(caseUpdates); result.Error != nil || result.RowsAffected != 1 {
				return quack.ErrAppealStateConflict
			}
			if err := tx.Model(&quack.CaseActionExecution{}).Where("case_id = ? AND status IN ?", item.ID, []quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRetrying}).Updates(map[string]any{"status": quack.ActionExecutionCancelled, "last_error_code": "case_voided", "last_error": "case was voided before enforcement", "finished_at": now, "next_retry_at": nil}).Error; err != nil {
				return fmt.Errorf("cancel appeal-voided case actions: %w", err)
			}
			if err := tx.Model(&quack.CaseNotification{}).Where("case_id = ? AND status IN ?", item.ID, []quack.NotificationStatus{quack.NotificationPending, quack.NotificationPrepared, quack.NotificationClaimed}).Updates(map[string]any{"status": quack.NotificationFailed, "last_error_code": "case_voided", "last_error": "case was voided before notification", "lease_token": "", "lease_expires_at": nil, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("cancel appeal-voided case notification: %w", err)
			}
			caseEvent := quack.CaseEvent{CaseID: item.ID, GuildID: item.GuildID, EventType: quack.CaseEventVoided, ActorDiscordUserID: params.ActorDiscordUserID, ActorType: "staff", Visibility: quack.EventVisibilityPublic, Body: "Case voided after appeal accepted", MetadataJSON: "{}"}
			if err := appendCaseEvent(tx, &caseEvent, now); err != nil {
				return err
			}
			if params.CaseAudit != nil {
				caseAudit := *params.CaseAudit
				caseAudit.ResourceID = item.ID
				if err := createAuditLogEntry(tx, &caseAudit, now); err != nil {
					return err
				}
			}
		}
		updated.Status = params.To
		updated.DecisionReason = params.Reason
		updated.ReviewedByDiscordUserID = params.ActorDiscordUserID
		updated.ReviewedAt = &now
		updated.Version++
		updated.UpdatedAt = now
		result = tx.Model(&appealV5Record{}).Where("id = ? AND version = ?", updated.ID, updated.Version-1).Updates(map[string]any{
			"status": updated.Status, "decision_reason": updated.DecisionReason,
			"reviewed_by_discord_user_id": updated.ReviewedByDiscordUserID,
			"reviewed_at":                 now, "version": updated.Version, "updated_at": now,
		})
		if result.Error != nil || result.RowsAffected != 1 {
			return quack.ErrAppealStateConflict
		}
		event := params.Event
		event.AppealID = updated.ID
		event.GuildID = updated.GuildID
		if err := prepareULIDModel(&event.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealEventRecord(event)).Error; err != nil {
			return err
		}
		notification := params.Notification
		notification.AppealID = updated.ID
		notification.EventID = event.ID
		notification.GuildID = updated.GuildID
		if err := prepareULIDModel(&notification.ULIDModel, now); err != nil {
			return err
		}
		if err := tx.Create(appealNotificationRecord(notification)).Error; err != nil {
			return err
		}
		audit := params.AppealAudit
		audit.ResourceID = updated.ID
		return createAuditLogEntry(tx, &audit, now)
	})
	if err != nil {
		return nil, err
	}
	return appealModel(updated), nil
}

func appealStatusIn(status quack.AppealStatus, allowed []quack.AppealStatus) bool {
	for _, candidate := range allowed {
		if status == candidate {
			return true
		}
	}
	return false
}

func isDuplicateError(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "duplicate") || strings.Contains(text, "unique constraint")
}
