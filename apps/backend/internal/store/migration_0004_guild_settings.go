package store

import (
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const migration0004Definition = `guild-settings-v1
schema: create guild_settings with one row per guild for core channels, notification branding, independent optional-module enablement, starter template identity, and one-time review notice state
compatibility: seed settings for every existing guild without changing guild, staff, template, case, event, action, attempt, appeal, ticket, or audit rows
defaults: channels and branding empty; optional modules disabled; starter template unset; starter review notice pending
rollback: drop only guild_settings; all pre-existing moderation and audit history remains unchanged`

// migration0004GuildSettings adds the guild-owned setup boundary without rewriting existing moderation history.
func migration0004GuildSettings() migration {
	return migration{
		Version:    4,
		Name:       "guild_settings",
		Definition: migration0004Definition,
		Source:     migration0004Source,
		Up:         applyGuildSettings,
		Down:       rollbackGuildSettings,
	}
}

// migration0004Guild is the frozen guild identity subset used to seed one settings row per existing guild.
type migration0004Guild struct {
	ID string
}

// TableName keeps migration reads on the existing guilds table.
func (migration0004Guild) TableName() string { return "guilds" }

// migration0004GuildSettingsRecord is the frozen storage shape created by migration 0004.
type migration0004GuildSettingsRecord struct {
	ID                                string     `gorm:"type:char(26);primaryKey"`
	CreatedAt                         time.Time  `gorm:"not null;index"`
	UpdatedAt                         time.Time  `gorm:"not null"`
	GuildID                           string     `gorm:"type:char(26);not null;uniqueIndex"`
	AuditMirrorChannelDiscordID       string     `gorm:"size:32;not null;default:''"`
	ManagedEvidenceChannelDiscordID   string     `gorm:"size:32;not null;default:''"`
	NotificationIntroduction          string     `gorm:"type:text;not null"`
	NotificationFooter                string     `gorm:"type:text;not null"`
	TicketsEnabled                    bool       `gorm:"not null;default:false"`
	GeneralLoggingEnabled             bool       `gorm:"not null;default:false"`
	HoneypotEnabled                   bool       `gorm:"not null;default:false"`
	StarterPolicyTemplateID           string     `gorm:"type:char(26);not null;default:''"`
	StarterPolicyNoticePending        bool       `gorm:"not null;default:true"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `gorm:"index"`
}

// TableName gives migration 0004's frozen record the product table name.
func (migration0004GuildSettingsRecord) TableName() string { return "guild_settings" }

// applyGuildSettings creates and seeds the settings table idempotently.
func applyGuildSettings(db *gorm.DB) error {
	migrator := withMySQLTableOptions(db).Migrator()
	if !migrator.HasTable(&migration0004GuildSettingsRecord{}) {
		if err := migrator.CreateTable(&migration0004GuildSettingsRecord{}); err != nil {
			return fmt.Errorf("create guild settings table: %w", err)
		}
	}

	var guilds []migration0004Guild
	if err := db.Find(&guilds).Error; err != nil {
		return fmt.Errorf("list guilds for settings compatibility: %w", err)
	}
	now := time.Now().UTC()
	for _, guild := range guilds {
		id := quack.NewID()
		row := migration0004GuildSettingsRecord{
			ID: id, CreatedAt: now, UpdatedAt: now, GuildID: guild.ID,
			StarterPolicyNoticePending: true,
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return fmt.Errorf("seed guild settings for guild %s: %w", guild.ID, err)
		}
	}
	return nil
}

// rollbackGuildSettings removes only migration-owned guild settings state.
func rollbackGuildSettings(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&migration0004GuildSettingsRecord{}) {
		return nil
	}
	if err := migrator.DropTable(&migration0004GuildSettingsRecord{}); err != nil {
		return fmt.Errorf("drop guild settings table: %w", err)
	}
	return nil
}
