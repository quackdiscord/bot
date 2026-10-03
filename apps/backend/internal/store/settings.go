package store

import (
	"context"
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
		for _, channel := range []*string{&r.AuditMirrorChannelDiscordID, &r.ManagedEvidenceChannelDiscordID} {
			if *channel == channelID {
				*channel = ""
				changed = true
			}
		}
		return changed
	})
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

// GetGuildAppealSettings returns a guild's appeal form, or nil when the guild
// uses the default form.
func (s *Store) GetGuildAppealSettings(ctx context.Context, guildID string) (*quack.GuildAppealSettings, error) {
	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	return findOne(query, "get appeal settings", appealSettingsRecord.model)
}

// UpdateGuildAppealSettings replaces the form future appeals use. Existing
// appeals keep the questions they were submitted with.
func (s *Store) UpdateGuildAppealSettings(ctx context.Context, params quack.UpdateGuildAppealSettingsParams) (*quack.GuildAppealSettings, error) {
	now := time.Now().UTC()
	var record appealSettingsRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).Where("guild_id = ?", params.Settings.GuildID), &record)
		if err != nil {
			return fmt.Errorf("get appeal settings: %w", err)
		}
		if !found {
			record = appealSettingsRecord{ID: quack.NewID(), CreatedAt: now, GuildID: params.Settings.GuildID}
		}
		record.QuestionsJSON = params.Settings.QuestionsJSON
		record.UpdatedByDiscordUserID = params.Settings.UpdatedByDiscordUserID
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("save appeal settings: %w", err)
		}
		return writeAudit(tx, &params.Audit, record.ID, now)
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
		AuditMirrorChannelDiscordID:       r.AuditMirrorChannelDiscordID,
		ManagedEvidenceChannelDiscordID:   r.ManagedEvidenceChannelDiscordID,
		NotificationIntroduction:          r.NotificationIntroduction,
		NotificationFooter:                r.NotificationFooter,
		StarterPolicyTemplateID:           r.StarterPolicyTemplateID,
		StarterPolicyNoticePending:        r.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: r.StarterPolicyNoticeAcknowledgedAt,
	}
}

func (r appealSettingsRecord) model() quack.GuildAppealSettings {
	return quack.GuildAppealSettings{
		ULIDModel:              ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                r.GuildID,
		QuestionsJSON:          r.QuestionsJSON,
		UpdatedByDiscordUserID: r.UpdatedByDiscordUserID,
	}
}
