package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// auditMirrorOutcomeUnknown is the LastError of a delivery found sending
// past its lease: the process may have posted it before stopping, so it is
// never retried.
const auditMirrorOutcomeUnknown = "delivery_outcome_unknown"

// queueAuditMirrorDelivery queues record, an important entry, for the audit
// mirror when its guild has a mirror channel set. It runs in the
// transaction that writes record, so it sees a channel set or cleared by
// the change being audited. Guilds without a channel get no row at all.
func queueAuditMirrorDelivery(tx *gorm.DB, record auditRecord, now time.Time) error {
	var channels []string
	if err := tx.Model(&guildSettingsRecord{}).Where("guild_id = ?", record.GuildID).
		Limit(1).Pluck("audit_mirror_channel_discord_id", &channels).Error; err != nil {
		return fmt.Errorf("read audit mirror channel: %w", err)
	}
	if len(channels) == 0 || strings.TrimSpace(channels[0]) == "" {
		return nil
	}
	delivery := auditMirrorDeliveryRecord{
		AuditEntryID:  record.ID,
		CreatedAt:     now,
		UpdatedAt:     now,
		GuildID:       record.GuildID,
		Status:        quack.AuditMirrorPending,
		NextAttemptAt: now,
	}
	if err := tx.Create(&delivery).Error; err != nil {
		return fmt.Errorf("queue audit mirror delivery: %w", err)
	}
	return nil
}

// ClaimAuditMirrorDeliveries leases up to limit due deliveries and returns
// them with their audit entries. Due means pending and past NextAttemptAt,
// or claimed by a lease that lapsed before sending began. Deliveries left
// sending past their lease fail as delivery_outcome_unknown first.
//
// The batch is fair across guilds: every guild with due work gets one slot
// before any gets a second, so one busy guild cannot hold back the rest.
// Within a guild, entries come oldest first.
func (s *Store) ClaimAuditMirrorDeliveries(ctx context.Context, limit int) ([]quack.AuditMirrorDelivery, error) {
	limit, _ = page(limit, 0)
	now := time.Now().UTC()
	token := quack.NewID()
	expires := now.Add(leaseDuration)
	var ids []string
	var claimed []auditMirrorDeliveryRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&auditMirrorDeliveryRecord{}).
			Where("status = ? AND lease_expires_at <= ?", quack.AuditMirrorSending, now).
			Updates(map[string]any{
				"status":           quack.AuditMirrorFailed,
				"last_error":       auditMirrorOutcomeUnknown,
				"lease_token":      "",
				"lease_expires_at": nil,
				"updated_at":       now,
			}).Error; err != nil {
			return fmt.Errorf("fail interrupted audit mirror deliveries: %w", err)
		}
		const due = `(status = @pending AND next_attempt_at <= @now) OR (status = @claimed AND lease_expires_at <= @now)`
		args := map[string]any{
			"pending": quack.AuditMirrorPending,
			"claimed": quack.AuditMirrorClaimed,
			"now":     now,
			"limit":   limit,
		}
		if err := tx.Raw(`
WITH ranked AS (
    SELECT audit_entry_id, guild_id,
           ROW_NUMBER() OVER (PARTITION BY guild_id ORDER BY audit_entry_id) AS guild_rank
      FROM audit_mirror_deliveries
     WHERE `+due+`
)
SELECT audit_entry_id
  FROM ranked
 ORDER BY guild_rank, audit_entry_id
 LIMIT @limit`, args).Scan(&ids).Error; err != nil {
			return fmt.Errorf("find due audit mirror deliveries: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		// The due condition is repeated so a row another claimer took
		// between the two statements is left alone.
		if err := tx.Model(&auditMirrorDeliveryRecord{}).
			Where("audit_entry_id IN ?", ids).Where(due, args).
			Updates(map[string]any{
				"status":           quack.AuditMirrorClaimed,
				"lease_token":      token,
				"lease_expires_at": expires,
				"updated_at":       now,
			}).Error; err != nil {
			return fmt.Errorf("claim audit mirror deliveries: %w", err)
		}
		return tx.Where("lease_token = ?", token).Find(&claimed).Error
	})
	if err != nil {
		return nil, err
	}
	if len(claimed) == 0 {
		return nil, nil
	}
	attempts := make(map[string]uint8, len(claimed))
	claimedIDs := make([]string, 0, len(claimed))
	for _, record := range claimed {
		attempts[record.AuditEntryID] = record.Attempts
		claimedIDs = append(claimedIDs, record.AuditEntryID)
	}
	var entries []auditRecord
	if err := s.db.WithContext(ctx).Where("id IN ?", claimedIDs).Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("load audit mirror entries: %w", err)
	}
	byID := make(map[string]auditRecord, len(entries))
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	// Audit entries are never deleted, so every claimed row has its entry.
	deliveries := make([]quack.AuditMirrorDelivery, 0, len(claimed))
	for _, id := range ids {
		count, ok := attempts[id]
		entry, found := byID[id]
		if !ok || !found {
			continue
		}
		deliveries = append(deliveries, quack.AuditMirrorDelivery{
			Entry:      entry.model(),
			Attempts:   int(count),
			LeaseToken: token,
		})
	}
	return deliveries, nil
}

// BeginAuditMirrorDelivery moves a claimed delivery to sending just before
// it goes to Discord, provided the caller's lease is still live. From here
// on the row is never claimed again.
func (s *Store) BeginAuditMirrorDelivery(ctx context.Context, auditEntryID, leaseToken string) error {
	now := time.Now().UTC()
	result := s.db.WithContext(ctx).Model(&auditMirrorDeliveryRecord{}).
		Where("audit_entry_id = ? AND status = ? AND lease_token = ? AND lease_expires_at > ?",
			auditEntryID, quack.AuditMirrorClaimed, leaseToken, now).
		Updates(map[string]any{"status": quack.AuditMirrorSending, "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("begin audit mirror delivery: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return quack.ErrAuditMirrorLeaseLost
	}
	return nil
}

// CompleteAuditMirrorDelivery records the outcome of a claimed or sending
// delivery the caller holds the lease for, and releases the lease. A
// give-up audit entry is written in the same transaction, unless another of
// the guild's deliveries has already failed since its last successful one:
// staff hear about an outage once, not once per entry.
func (s *Store) CompleteAuditMirrorDelivery(ctx context.Context, params quack.CompleteAuditMirrorDeliveryParams) error {
	switch params.Status {
	case quack.AuditMirrorPending, quack.AuditMirrorDelivered, quack.AuditMirrorSkipped, quack.AuditMirrorFailed:
	default:
		return fmt.Errorf("complete audit mirror delivery: invalid status %q", params.Status)
	}
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record auditMirrorDeliveryRecord
		found, err := first(forUpdate(tx).Where("audit_entry_id = ? AND lease_token = ? AND status IN ?",
			params.AuditEntryID, params.LeaseToken,
			[]quack.AuditMirrorDeliveryStatus{quack.AuditMirrorClaimed, quack.AuditMirrorSending}), &record)
		if err != nil {
			return fmt.Errorf("get audit mirror delivery: %w", err)
		}
		if !found {
			return quack.ErrAuditMirrorLeaseLost
		}
		if params.Status == quack.AuditMirrorFailed && params.GiveUpAudit != nil {
			reported, err := auditMirrorOutageReported(tx, record)
			if err != nil {
				return err
			}
			if !reported {
				entry := *params.GiveUpAudit
				entry.GuildID = record.GuildID
				if err := createAuditLogEntry(tx, &entry, now); err != nil {
					return err
				}
			}
		}
		updates := map[string]any{
			"status":               params.Status,
			"attempts":             min(max(params.Attempts, 0), 255),
			"last_error":           redactFailureReason(params.LastError),
			"delivered_message_id": params.DeliveredMessageID,
			"lease_token":          "",
			"lease_expires_at":     nil,
			"updated_at":           now,
		}
		if params.Status == quack.AuditMirrorPending {
			updates["next_attempt_at"] = params.NextAttemptAt.UTC()
		}
		if err := tx.Model(&auditMirrorDeliveryRecord{}).
			Where("audit_entry_id = ?", record.AuditEntryID).Updates(updates).Error; err != nil {
			return fmt.Errorf("complete audit mirror delivery: %w", err)
		}
		return nil
	})
}

// auditMirrorOutageReported reports whether another of record's guild's
// deliveries was given up since the guild's latest successful delivery.
// Deliveries whose outcome is unknown do not count; nothing was reported
// for them.
func auditMirrorOutageReported(tx *gorm.DB, record auditMirrorDeliveryRecord) (bool, error) {
	var delivered auditMirrorDeliveryRecord
	hasDelivered, err := first(tx.Where("guild_id = ? AND status = ?", record.GuildID, quack.AuditMirrorDelivered).
		Order("updated_at DESC"), &delivered)
	if err != nil {
		return false, fmt.Errorf("find last audit mirror delivery: %w", err)
	}
	query := tx.Where("guild_id = ? AND status = ? AND audit_entry_id <> ? AND last_error <> ?",
		record.GuildID, quack.AuditMirrorFailed, record.AuditEntryID, auditMirrorOutcomeUnknown)
	if hasDelivered {
		query = query.Where("updated_at >= ?", delivered.UpdatedAt)
	}
	var failed auditMirrorDeliveryRecord
	reported, err := first(query, &failed)
	if err != nil {
		return false, fmt.Errorf("find earlier audit mirror failure: %w", err)
	}
	return reported, nil
}
