package store

import (
	"context"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ClaimPendingAppealNotifications atomically leases bounded outbox work so concurrent dispatchers cannot deliver one event twice.
func (s *Store) ClaimPendingAppealNotifications(ctx context.Context, limit int) ([]quack.AppealNotification, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if limit < 1 || limit > 100 {
		return nil, errors.New("appeal notification claim limit is invalid")
	}
	now := time.Now().UTC()
	token := quack.NewID()
	expiresAt := now.Add(2 * time.Minute)
	var records []AppealNotificationRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("status = ? OR (status = ? AND lease_expires_at <= ?)", quack.AppealNotificationPending, quack.AppealNotificationClaimed, now).
			Order("created_at ASC").Limit(limit).Find(&records)
		if result.Error != nil || len(records) == 0 {
			return result.Error
		}
		ids := make([]string, 0, len(records))
		for index := range records {
			ids = append(ids, records[index].ID)
			records[index].Status = quack.AppealNotificationClaimed
			records[index].LeaseToken = token
			records[index].LeaseExpiresAt = &expiresAt
			records[index].UpdatedAt = now
		}
		result = tx.Model(&AppealNotificationRecord{}).Where("id IN ?", ids).Updates(map[string]any{"status": quack.AppealNotificationClaimed, "lease_token": token, "lease_expires_at": expiresAt, "updated_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(records)) {
			return quack.ErrAppealStateConflict
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	items := make([]quack.AppealNotification, 0, len(records))
	for _, record := range records {
		items = append(items, appealNotificationModel(record))
	}
	return items, nil
}

// CompleteAppealNotification records delivery without changing the underlying appeal timeline.
func (s *Store) CompleteAppealNotification(ctx context.Context, params quack.CompleteAppealNotificationParams) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	if params.Status != quack.AppealNotificationSent && params.Status != quack.AppealNotificationFailed {
		return errors.New("appeal notification completion status is invalid")
	}
	result := s.db.WithContext(ctx).Model(&AppealNotificationRecord{}).Where("id = ? AND status = ? AND lease_token = ?", params.NotificationID, quack.AppealNotificationClaimed, params.LeaseToken).Updates(map[string]any{"status": params.Status, "delivery_message_id": params.DeliveryMessageID, "last_error_code": params.ErrorCode, "lease_token": "", "lease_expires_at": nil, "updated_at": time.Now().UTC()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return quack.ErrAppealStateConflict
	}
	return nil
}
