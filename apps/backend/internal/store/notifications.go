package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// claimableNotification matches a case notification a worker may claim: not
// yet attempted, or claimed by a worker whose lease expired before it began
// sending. A notification in the sending state is never reclaimed, because
// the DM may already have gone out.
const claimableNotification = "(status IN (?, ?) OR (status = ? AND lease_expires_at <= ?))"

// PrepareCaseNotification records the DM channel opened before a kick or ban
// removes the member's last shared guild. With no channel it records why the
// channel could not be opened. Only pending notifications change.
func (s *Store) PrepareCaseNotification(ctx context.Context, caseID, channelID, errorMessage string) error {
	updates := map[string]any{"prepared_channel_discord_id": channelID, "updated_at": time.Now().UTC()}
	if channelID != "" {
		updates["status"] = quack.NotificationPrepared
	} else {
		updates["last_error_code"] = "dm_prepare_failed"
		updates["last_error"] = errorMessage
	}
	return s.db.WithContext(ctx).Model(&caseNotificationRecord{}).
		Where("case_id = ? AND status = ?", caseID, quack.NotificationPending).
		Updates(updates).Error
}

// ClaimCaseNotification leases the case's notification for sending, or
// returns nil when it is not claimable or the case still has enforcement to
// do; the DM always describes the settled outcome.
func (s *Store) ClaimCaseNotification(ctx context.Context, params quack.ClaimCaseNotificationParams) (*quack.CaseNotification, error) {
	now := time.Now().UTC()
	var claimed *quack.CaseNotification
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active int64
		if err := tx.Model(&executionRecord{}).
			Where("case_id = ? AND status IN ?", params.CaseID, activeExecutionStatuses).
			Count(&active).Error; err != nil {
			return fmt.Errorf("count active case actions: %w", err)
		}
		if active > 0 {
			return nil
		}
		var record caseNotificationRecord
		found, err := first(forUpdate(tx).Where("case_id = ?", params.CaseID).
			Where(claimableNotification,
				quack.NotificationPending, quack.NotificationPrepared, quack.NotificationClaimed, now), &record)
		if err != nil || !found {
			return wrap("claim case notification", err)
		}
		expires := now.Add(leaseDuration)
		record.Status = quack.NotificationClaimed
		record.AttemptCount++
		record.LeaseToken = quack.NewID()
		record.LeaseExpiresAt = &expires
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("claim case notification: %w", err)
		}
		n := record.model()
		claimed = &n
		return nil
	})
	return claimed, err
}

// BeginCaseNotificationDelivery moves a claimed notification to sending just
// before the DM goes out. After this point the notification is never
// reclaimed, so a crash mid-send cannot produce a second DM.
func (s *Store) BeginCaseNotificationDelivery(ctx context.Context, notificationID, leaseToken string) error {
	result := s.db.WithContext(ctx).Model(&caseNotificationRecord{}).
		Where("id = ? AND lease_token = ? AND status = ?", notificationID, leaseToken, quack.NotificationClaimed).
		Updates(map[string]any{"status": quack.NotificationSending, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return fmt.Errorf("begin case notification delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("begin case notification delivery: %w", errStaleLease)
	}
	return nil
}

// CompleteCaseNotification records the outcome of a send that the caller
// still holds the lease for, with its timeline event and audit entry.
func (s *Store) CompleteCaseNotification(ctx context.Context, params quack.CompleteCaseNotificationParams) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record caseNotificationRecord
		found, err := first(forUpdate(tx).Where("id = ? AND lease_token = ? AND status = ?",
			params.NotificationID, params.LeaseToken, quack.NotificationSending), &record)
		if err != nil {
			return fmt.Errorf("get case notification: %w", err)
		}
		if !found {
			return fmt.Errorf("complete case notification: %w", errStaleLease)
		}
		record.Status = params.Status
		record.PreparedChannelDiscordID = params.PreparedChannelDiscordID
		record.RenderedMessage = params.RenderedMessage
		record.DeliveryMessageDiscordID = params.DeliveryMessageDiscordID
		record.LastErrorCode = params.ErrorCode
		record.LastError = params.ErrorMessage
		record.LeaseToken = ""
		record.LeaseExpiresAt = nil
		record.UpdatedAt = now
		if params.Status == quack.NotificationSent {
			record.SentAt = &now
		}
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("complete case notification: %w", err)
		}
		var c caseRecord
		found, err = first(tx.Where("id = ?", record.CaseID), &c)
		if err != nil {
			return fmt.Errorf("get case for notification: %w", err)
		}
		if !found {
			return fmt.Errorf("get case for notification: case %s not found", record.CaseID)
		}
		if err := appendCaseEvent(tx, &quack.CaseEvent{
			CaseID:       c.ID,
			GuildID:      c.GuildID,
			EventType:    params.EventType,
			Visibility:   quack.EventVisibilityPublic,
			Body:         "Member notification delivery updated",
			MetadataJSON: jsonObject(map[string]any{"status": params.Status}),
		}, now); err != nil {
			return err
		}
		action, result := "case_notification.sent", quack.AuditResultSuccess
		if params.Status != quack.NotificationSent {
			action, result = "case_notification.failed", quack.AuditResultFailure
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{
			GuildID:       c.GuildID,
			Source:        quack.AuditSourceSystem,
			Action:        action,
			ResourceType:  "case_notification",
			ResourceID:    record.ID,
			Result:        result,
			FailureReason: params.ErrorMessage,
			CorrelationID: c.CorrelationID,
			MetadataJSON:  jsonObject(map[string]any{"case_id": c.ID, "status": params.Status}),
		}, now)
	})
}

// ClaimPendingAppealNotifications leases up to limit appeal outbox rows,
// oldest first, including rows whose previous lease expired.
func (s *Store) ClaimPendingAppealNotifications(ctx context.Context, limit int) ([]quack.AppealNotification, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("appeal notification claim limit is invalid")
	}
	now := time.Now().UTC()
	token := quack.NewID()
	expires := now.Add(leaseDuration)
	var records []appealNotificationRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := forUpdate(tx).
			Where("status = ? OR (status = ? AND lease_expires_at <= ?)",
				quack.AppealNotificationPending, quack.AppealNotificationClaimed, now).
			Order("created_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
			return fmt.Errorf("find appeal notifications: %w", err)
		}
		if len(records) == 0 {
			return nil
		}
		ids := make([]string, len(records))
		for i := range records {
			ids[i] = records[i].ID
			records[i].Status = quack.AppealNotificationClaimed
			records[i].LeaseToken = token
			records[i].LeaseExpiresAt = &expires
			records[i].UpdatedAt = now
		}
		result := tx.Model(&appealNotificationRecord{}).Where("id IN ?", ids).Updates(map[string]any{
			"status":           quack.AppealNotificationClaimed,
			"lease_token":      token,
			"lease_expires_at": expires,
			"updated_at":       now,
		})
		if result.Error != nil {
			return fmt.Errorf("claim appeal notifications: %w", result.Error)
		}
		if result.RowsAffected != int64(len(records)) {
			return quack.ErrAppealStateConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return modelsOf(records, appealNotificationRecord.model), nil
}

// CompleteAppealNotification records a delivery outcome for a notification
// the caller still holds the lease for.
func (s *Store) CompleteAppealNotification(ctx context.Context, params quack.CompleteAppealNotificationParams) error {
	if params.Status != quack.AppealNotificationSent && params.Status != quack.AppealNotificationFailed {
		return errors.New("appeal notification completion status is invalid")
	}
	result := s.db.WithContext(ctx).Model(&appealNotificationRecord{}).
		Where("id = ? AND status = ? AND lease_token = ?",
			params.NotificationID, quack.AppealNotificationClaimed, params.LeaseToken).
		Updates(map[string]any{
			"status":              params.Status,
			"delivery_message_id": params.DeliveryMessageID,
			"last_error_code":     params.ErrorCode,
			"lease_token":         "",
			"lease_expires_at":    nil,
			"updated_at":          time.Now().UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("complete appeal notification: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return quack.ErrAppealStateConflict
	}
	return nil
}

func newCaseNotificationRecord(n quack.CaseNotification) caseNotificationRecord {
	return caseNotificationRecord{
		ID:                       n.ID,
		CreatedAt:                n.CreatedAt,
		UpdatedAt:                n.UpdatedAt,
		CaseID:                   n.CaseID,
		Status:                   n.Status,
		PreparedChannelDiscordID: n.PreparedChannelDiscordID,
		RenderedMessage:          n.RenderedMessage,
		DeliveryMessageDiscordID: n.DeliveryMessageDiscordID,
		AttemptCount:             n.AttemptCount,
		LastErrorCode:            n.LastErrorCode,
		LastError:                n.LastError,
		LeaseToken:               n.LeaseToken,
		LeaseExpiresAt:           n.LeaseExpiresAt,
		SentAt:                   n.SentAt,
	}
}

func (r caseNotificationRecord) model() quack.CaseNotification {
	return quack.CaseNotification{
		ULIDModel:                ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		CaseID:                   r.CaseID,
		Status:                   r.Status,
		PreparedChannelDiscordID: r.PreparedChannelDiscordID,
		RenderedMessage:          r.RenderedMessage,
		DeliveryMessageDiscordID: r.DeliveryMessageDiscordID,
		AttemptCount:             r.AttemptCount,
		LastErrorCode:            r.LastErrorCode,
		LastError:                r.LastError,
		LeaseToken:               r.LeaseToken,
		LeaseExpiresAt:           r.LeaseExpiresAt,
		SentAt:                   r.SentAt,
	}
}

func newAppealNotificationRecord(n quack.AppealNotification) appealNotificationRecord {
	return appealNotificationRecord{
		ID:                  n.ID,
		CreatedAt:           n.CreatedAt,
		UpdatedAt:           n.UpdatedAt,
		AppealID:            n.AppealID,
		EventID:             n.EventID,
		GuildID:             n.GuildID,
		TargetDiscordUserID: n.TargetDiscordUserID,
		Audience:            n.Audience,
		Status:              n.Status,
		Body:                n.Body,
		DeliveryMessageID:   n.DeliveryMessageID,
		LastErrorCode:       n.LastErrorCode,
		LeaseToken:          n.LeaseToken,
		LeaseExpiresAt:      n.LeaseExpiresAt,
	}
}

func (r appealNotificationRecord) model() quack.AppealNotification {
	return quack.AppealNotification{
		ULIDModel:           ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		AppealID:            r.AppealID,
		EventID:             r.EventID,
		GuildID:             r.GuildID,
		TargetDiscordUserID: r.TargetDiscordUserID,
		Audience:            r.Audience,
		Status:              r.Status,
		Body:                r.Body,
		DeliveryMessageID:   r.DeliveryMessageID,
		LastErrorCode:       r.LastErrorCode,
		LeaseToken:          r.LeaseToken,
		LeaseExpiresAt:      r.LeaseExpiresAt,
	}
}
