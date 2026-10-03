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

// GetGuildSettings returns the single guild-owned settings record, if one exists.
func (s *Store) GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record GuildSettingsRecord
	result := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Limit(1).Find(&record)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild settings: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	settings := guildSettingsModelFromRecord(record)
	return &settings, nil
}

// UpdateGuildSettings atomically replaces validated mutable settings and appends success audit evidence.
func (s *Store) UpdateGuildSettings(ctx context.Context, params quack.UpdateGuildSettingsParams) (*quack.GuildSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record GuildSettingsRecord
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", params.Settings.GuildID).First(&record).Error; err != nil {
			return fmt.Errorf("get guild settings for update: %w", err)
		}
		record.AuditMirrorChannelDiscordID = params.Settings.AuditMirrorChannelDiscordID
		record.ManagedEvidenceChannelDiscordID = params.Settings.ManagedEvidenceChannelDiscordID
		record.NotificationIntroduction = params.Settings.NotificationIntroduction
		record.NotificationFooter = params.Settings.NotificationFooter
		record.TicketsEnabled = params.Settings.TicketsEnabled
		record.GeneralLoggingEnabled = params.Settings.GeneralLoggingEnabled
		record.HoneypotEnabled = params.Settings.HoneypotEnabled
		record.StarterPolicyNoticePending = params.Settings.StarterPolicyNoticePending
		record.StarterPolicyNoticeAcknowledgedAt = params.Settings.StarterPolicyNoticeAcknowledgedAt
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("update guild settings: %w", err)
		}
		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	settings := guildSettingsModelFromRecord(record)
	return &settings, nil
}

// ClearGuildChannelReferences atomically clears every core settings reference to a deleted or invalid Discord channel.
func (s *Store) ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *quack.AuditLogEntry) (*quack.GuildSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var record GuildSettingsRecord
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guildID).First(&record).Error; err != nil {
			return fmt.Errorf("get guild settings for channel repair: %w", err)
		}
		changed := false
		if record.AuditMirrorChannelDiscordID == channelID {
			record.AuditMirrorChannelDiscordID = ""
			changed = true
		}
		if record.ManagedEvidenceChannelDiscordID == channelID {
			record.ManagedEvidenceChannelDiscordID = ""
			changed = true
		}
		if !changed {
			return nil
		}
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("clear guild channel references: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.GuildID = guildID
			entry.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	settings := guildSettingsModelFromRecord(record)
	return &settings, nil
}

// BootstrapGuild atomically refreshes guild lifecycle state and creates the one-time exact starter policy.
func (s *Store) BootstrapGuild(ctx context.Context, params quack.BootstrapGuildParams) (*quack.BootstrapGuildResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	result := &quack.BootstrapGuildResult{}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild quack.Guild
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("discord_guild_id = ?", params.DiscordGuildID).Limit(1).Find(&guild)
		if query.Error != nil {
			return fmt.Errorf("get guild for bootstrap: %w", query.Error)
		}
		wasActive := query.RowsAffected > 0 && guild.IsActive
		if query.RowsAffected == 0 {
			guild = quack.Guild{DiscordGuildID: params.DiscordGuildID}
			if err := prepareULIDModel(&guild.ULIDModel, now); err != nil {
				return fmt.Errorf("prepare bootstrap guild: %w", err)
			}
			result.GuildCreated = true
		}
		guild.Name = params.Name
		guild.IconURL = params.IconURL
		guild.OwnerDiscordUserID = params.OwnerDiscordUserID
		guild.IsActive = true
		guild.UpdatedAt = now
		if query.RowsAffected == 0 {
			if err := tx.Create(&guild).Error; err != nil {
				return fmt.Errorf("create bootstrap guild: %w", err)
			}
		} else if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("refresh bootstrap guild: %w", err)
		}

		var settingsRecord GuildSettingsRecord
		settingsQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("guild_id = ?", guild.ID).Limit(1).Find(&settingsRecord)
		if settingsQuery.Error != nil {
			return fmt.Errorf("get bootstrap settings: %w", settingsQuery.Error)
		}
		if settingsQuery.RowsAffected == 0 {
			settingsRecord = GuildSettingsRecord{GuildID: guild.ID, StarterPolicyNoticePending: true}
			if err := prepareULIDRecord(&settingsRecord.ULIDModelRecord, now); err != nil {
				return fmt.Errorf("prepare bootstrap settings: %w", err)
			}
			if err := tx.Create(&settingsRecord).Error; err != nil {
				return fmt.Errorf("create bootstrap settings: %w", err)
			}
		}

		if settingsRecord.StarterPolicyTemplateID == "" {
			starter, created, err := ensureStarterPolicy(tx, guild.ID, params.Starter, now)
			if err != nil {
				return err
			}
			settingsRecord.StarterPolicyTemplateID = starter.Template.ID
			settingsRecord.StarterPolicyNoticePending = true
			settingsRecord.UpdatedAt = now
			if err := tx.Save(&settingsRecord).Error; err != nil {
				return fmt.Errorf("bind starter policy to guild settings: %w", err)
			}
			result.StarterTemplate = *starter
			result.StarterTemplateCreated = created
			if created {
				if err := createSystemLifecycleAudit(tx, guild.ID, "case_template.bootstrap", "case_template", starter.Template.ID, now); err != nil {
					return err
				}
			}
		} else {
			starter, err := getCaseTemplateExpanded(tx, guild.ID, settingsRecord.StarterPolicyTemplateID)
			if err != nil {
				return err
			}
			if starter == nil {
				return errors.New("configured starter policy template is missing")
			}
			result.StarterTemplate = *starter
		}

		channelReferencesRepaired := false
		if params.KnownChannelDiscordIDs != nil {
			known := make(map[string]struct{}, len(params.KnownChannelDiscordIDs))
			for _, id := range params.KnownChannelDiscordIDs {
				known[id] = struct{}{}
			}
			if settingsRecord.AuditMirrorChannelDiscordID != "" {
				if _, ok := known[settingsRecord.AuditMirrorChannelDiscordID]; !ok {
					settingsRecord.AuditMirrorChannelDiscordID = ""
					channelReferencesRepaired = true
				}
			}
			if settingsRecord.ManagedEvidenceChannelDiscordID != "" {
				if _, ok := known[settingsRecord.ManagedEvidenceChannelDiscordID]; !ok {
					settingsRecord.ManagedEvidenceChannelDiscordID = ""
					channelReferencesRepaired = true
				}
			}
			settingsRecord.UpdatedAt = now
			if err := tx.Save(&settingsRecord).Error; err != nil {
				return fmt.Errorf("repair bootstrap channel references: %w", err)
			}
		}
		if channelReferencesRepaired {
			if err := createSystemLifecycleAudit(tx, guild.ID, "guild_settings.channel_references.repaired", "guild_settings", settingsRecord.ID, now); err != nil {
				return err
			}
		}
		if result.GuildCreated || !wasActive {
			if err := createSystemLifecycleAudit(tx, guild.ID, "guild.lifecycle.bootstrap", "guild", guild.ID, now); err != nil {
				return err
			}
		}

		result.Guild = guild
		result.Settings = guildSettingsModelFromRecord(settingsRecord)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// createSystemLifecycleAudit appends adapter-attributed lifecycle evidence inside the caller's transaction.
func createSystemLifecycleAudit(tx *gorm.DB, guildID, action, resourceType, resourceID string, now time.Time) error {
	return createAuditLogEntry(tx, &quack.AuditLogEntry{
		GuildID: guildID, ActorDiscordUserID: "quack-system", Source: quack.AuditSourceDiscord,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Result: quack.AuditResultSuccess, MetadataJSON: "{}",
	}, now)
}

// DeactivateGuild marks a departed guild inactive while retaining every owned record and appends lifecycle audit evidence.
func (s *Store) DeactivateGuild(ctx context.Context, discordGuildID string, audit *quack.AuditLogEntry) (*quack.Guild, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var guild quack.Guild
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("discord_guild_id = ?", discordGuildID).First(&guild).Error; err != nil {
			return fmt.Errorf("get guild for deactivation: %w", err)
		}
		guild.IsActive = false
		guild.UpdatedAt = now
		if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("deactivate guild: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.GuildID = guild.ID
			entry.ResourceID = guild.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &guild, nil
}

// ensureStarterPolicy stores starter as the guild's starter template, or
// returns the existing one on a repeated bootstrap.
func ensureStarterPolicy(tx *gorm.DB, guildID string, starter quack.ExpandedCaseTemplate, now time.Time) (*quack.ExpandedCaseTemplate, bool, error) {
	if starter.Template.Slug == "" {
		return nil, false, errors.New("starter policy is required")
	}
	var existing CaseTemplateRecord
	query := tx.Where("guild_id = ? AND slug = ?", guildID, starter.Template.Slug).Limit(1).Find(&existing)
	if query.Error != nil {
		return nil, false, fmt.Errorf("find starter policy: %w", query.Error)
	}
	if query.RowsAffected > 0 {
		expanded, err := getCaseTemplateExpanded(tx, guildID, existing.ID)
		if err != nil {
			return nil, false, err
		}
		if expanded == nil || !quack.IsStarterTemplate(*expanded) {
			return nil, false, fmt.Errorf("%s slug is already used by a non-starter policy", starter.Template.Slug)
		}
		return expanded, false, nil
	}

	template := starter.Template
	template.ID = ""
	template.GuildID = guildID
	if err := prepareULIDModel(&template.ULIDModel, now); err != nil {
		return nil, false, fmt.Errorf("prepare starter policy: %w", err)
	}
	record := caseTemplateRecordFromModel(template)
	if err := tx.Select("*").Create(&record).Error; err != nil {
		return nil, false, fmt.Errorf("create starter policy: %w", err)
	}
	if err := createTemplateLevels(tx, template.ID, starter.Levels, now); err != nil {
		return nil, false, err
	}
	expanded, err := getCaseTemplateExpanded(tx, guildID, template.ID)
	return expanded, true, err
}

// guildSettingsModelFromRecord maps adapter storage into the persistence-free settings model.
func guildSettingsModelFromRecord(record GuildSettingsRecord) quack.GuildSettings {
	return quack.GuildSettings{
		ULIDModel: quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt},
		GuildID:   record.GuildID, AuditMirrorChannelDiscordID: record.AuditMirrorChannelDiscordID,
		ManagedEvidenceChannelDiscordID: record.ManagedEvidenceChannelDiscordID,
		NotificationIntroduction:        record.NotificationIntroduction, NotificationFooter: record.NotificationFooter,
		TicketsEnabled: record.TicketsEnabled, GeneralLoggingEnabled: record.GeneralLoggingEnabled,
		HoneypotEnabled: record.HoneypotEnabled, StarterPolicyTemplateID: record.StarterPolicyTemplateID,
		StarterPolicyNoticePending:        record.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: record.StarterPolicyNoticeAcknowledgedAt,
	}
}

// prepareULIDRecord initializes adapter-owned records without exposing storage tags to the core model.
func prepareULIDRecord(record *ULIDModelRecord, now time.Time) error {
	modelValue := quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}
	if err := prepareULIDModel(&modelValue, now); err != nil {
		return err
	}
	record.ID, record.CreatedAt, record.UpdatedAt = modelValue.ID, modelValue.CreatedAt, modelValue.UpdatedAt
	return nil
}
