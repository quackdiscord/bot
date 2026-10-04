package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// GetGuildSettings returns a guild's settings, or nil before bootstrap.
func (s *Store) GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error) {
	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	return findOne(query, "get guild settings", guildSettingsRecord.model)
}

// UpdateGuildSettings replaces a guild's editable settings. The starter
// template binding is not editable and is left alone.
func (s *Store) UpdateGuildSettings(ctx context.Context, params quack.UpdateGuildSettingsParams) (*quack.GuildSettings, error) {
	in := params.Settings
	return s.updateSettings(ctx, in.GuildID, params.Audit, func(r *guildSettingsRecord) bool {
		r.AppealQueueChannelDiscordID = in.AppealQueueChannelDiscordID
		r.AppealRejoinURL = in.AppealRejoinURL
		r.AppealReviewReasonRequired = in.AppealReviewReasonRequired
		r.AuditMirrorChannelDiscordID = in.AuditMirrorChannelDiscordID
		r.ManagedEvidenceChannelDiscordID = in.ManagedEvidenceChannelDiscordID
		r.NotificationIntroduction = in.NotificationIntroduction
		r.NotificationFooter = in.NotificationFooter
		r.StarterPolicyNoticePending = in.StarterPolicyNoticePending
		r.StarterPolicyNoticeAcknowledgedAt = in.StarterPolicyNoticeAcknowledgedAt
		return true
	})
}

// ClearGuildChannelReferences unsets every setting that points at a deleted
// or unusable channel. Nothing is written or audited when none did.
func (s *Store) ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *quack.AuditLogEntry) (*quack.GuildSettings, error) {
	if audit != nil {
		entry := *audit
		entry.GuildID = guildID
		audit = &entry
	}
	return s.updateSettings(ctx, guildID, audit, func(r *guildSettingsRecord) bool {
		changed := false
		for _, channel := range []*string{&r.AppealQueueChannelDiscordID, &r.AuditMirrorChannelDiscordID, &r.ManagedEvidenceChannelDiscordID} {
			if *channel == channelID {
				*channel = ""
				changed = true
			}
		}
		return changed
	})
}

// SetManagedEvidenceChannel records next as the guild's evidence channel
// only while the setting is still expected, and returns the channel now
// recorded. A concurrent change wins and is returned instead, so ensuring
// the channel never overwrites another settings update.
func (s *Store) SetManagedEvidenceChannel(ctx context.Context, guildID, expected, next string, audit *quack.AuditLogEntry) (string, error) {
	if next == "" {
		return "", errors.New("evidence channel is required")
	}
	settings, err := s.updateSettings(ctx, guildID, audit, func(r *guildSettingsRecord) bool {
		if r.ManagedEvidenceChannelDiscordID != expected || r.ManagedEvidenceChannelDiscordID == next {
			return false
		}
		r.ManagedEvidenceChannelDiscordID = next
		return true
	})
	if err != nil {
		return "", err
	}
	return settings.ManagedEvidenceChannelDiscordID, nil
}

// updateSettings locks a guild's settings row, applies change, and saves and
// audits the row when change reports it changed something.
func (s *Store) updateSettings(ctx context.Context, guildID string, audit *quack.AuditLogEntry, change func(*guildSettingsRecord) bool) (*quack.GuildSettings, error) {
	var record guildSettingsRecord
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).Where("guild_id = ?", guildID), &record)
		if err != nil {
			return fmt.Errorf("get guild settings: %w", err)
		}
		if !found {
			return fmt.Errorf("get guild settings: %w", quack.ErrGuildSettingsNotFound)
		}
		if !change(&record) {
			return nil
		}
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("save guild settings: %w", err)
		}
		return writeAudit(tx, audit, record.ID, now)
	})
	if err != nil {
		return nil, err
	}
	settings := record.model()
	return &settings, nil
}

func (r guildSettingsRecord) model() quack.GuildSettings {
	return quack.GuildSettings{
		ULIDModel:                         ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                           r.GuildID,
		AppealQueueChannelDiscordID:       r.AppealQueueChannelDiscordID,
		AppealRejoinURL:                   r.AppealRejoinURL,
		AppealReviewReasonRequired:        r.AppealReviewReasonRequired,
		AuditMirrorChannelDiscordID:       r.AuditMirrorChannelDiscordID,
		ManagedEvidenceChannelDiscordID:   r.ManagedEvidenceChannelDiscordID,
		NotificationIntroduction:          r.NotificationIntroduction,
		NotificationFooter:                r.NotificationFooter,
		StarterPolicyTemplateID:           r.StarterPolicyTemplateID,
		StarterPolicyNoticePending:        r.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: r.StarterPolicyNoticeAcknowledgedAt,
	}
}
