package honeypot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// warningRefreshBatch caps the guilds one refresh pass updates.
	warningRefreshBatch = 8
	// warningRetryDelay is how long a failed refresh waits.
	warningRetryDelay = 30 * time.Second
	// warningSeedPage is the page size for queueing every enabled guild's
	// refresh after a restart.
	warningSeedPage = 32
)

// WarningRefresh is a guild's pending warning update, one row per guild, so
// any number of incidents coalesce into one edit. Revision tells a request
// made during a refresh from the one being served. SendIdentity fences the
// non-idempotent post of a replacement warning: while it is set for the
// current channel and warning, Quack does not post again.
type WarningRefresh struct {
	GuildID       string    `gorm:"type:char(26);primaryKey"`
	Revision      string    `gorm:"type:char(26);not null"`
	Pending       bool      `gorm:"not null;index:idx_honeypot_warning_due,priority:1"`
	NextAttemptAt time.Time `gorm:"not null;index:idx_honeypot_warning_due,priority:2"`
	SendIdentity  string    `gorm:"size:64;not null"`
}

// TableName returns the honeypot_warning_refreshes table.
func (WarningRefresh) TableName() string { return "honeypot_warning_refreshes" }

// ErrWarningDeliveryUnknown reports a replacement warning whose post may
// have gone through. Quack will not post another until an admin checks the
// channel; changing the warning text is no proof the last post is missing.
var ErrWarningDeliveryUnknown = errors.New("honeypot warning delivery is unconfirmed; inspect the configured channel")

// RequestWarningRefresh asks for the guild's warning to be updated. It
// keeps any send fence and retry backoff already in place.
func (s *Service) RequestWarningRefresh(ctx context.Context, guildID string) error {
	return requestWarningRefresh(ctx, s.store.db, guildID)
}

// requestWarningRefresh takes db so an incident's completion and its refresh
// request commit together.
func requestWarningRefresh(ctx context.Context, db *gorm.DB, guildID string) error {
	row := WarningRefresh{GuildID: guildID, Revision: ulid.Make().String(), Pending: true, NextAttemptAt: time.Now().UTC()}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "guild_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"revision", "pending"}),
	}).Create(&row).Error
}

// WarningRefreshes returns a bounded batch of refreshes due at now.
func (s *Service) WarningRefreshes(ctx context.Context, now time.Time) ([]WarningRefresh, error) {
	var rows []WarningRefresh
	err := s.store.db.WithContext(ctx).
		Where("pending = ? AND next_attempt_at <= ?", true, now.UTC()).
		Order("next_attempt_at, guild_id").Limit(warningRefreshBatch).Find(&rows).Error
	return rows, err
}

// CompleteWarningRefresh finishes row. Success clears the request unless a
// newer one arrived meanwhile; failure backs off either way, so a broken
// channel is not retried in a hot loop.
func (s *Service) CompleteWarningRefresh(ctx context.Context, row WarningRefresh, failed bool) error {
	query := s.store.db.WithContext(ctx).Model(&WarningRefresh{}).Where("guild_id = ?", row.GuildID)
	if failed {
		return query.Update("next_attempt_at", time.Now().UTC().Add(warningRetryDelay)).Error
	}
	return query.Where("revision = ?", row.Revision).Update("pending", false).Error
}

// ConfiguredWarningGuilds returns a page of guilds with the honeypot on,
// after the guild ID after, for queueing every warning's refresh at startup.
func (s *Service) ConfiguredWarningGuilds(ctx context.Context, after string) ([]string, error) {
	var ids []string
	err := s.store.db.WithContext(ctx).Model(&modules.Configuration{}).
		Where("module_id = ? AND enabled = ? AND guild_id > ?", modules.Honeypots, true, after).
		Order("guild_id").Limit(warningSeedPage).Pluck("guild_id", &ids).Error
	return ids, err
}

// warningSendIdentity binds a replacement post to the channel and the
// warning it replaces, so pointing the honeypot elsewhere or recording a new
// warning lifts the fence.
func warningSendIdentity(settings Settings) string {
	sum := sha256.Sum256([]byte(settings.ChannelDiscordID + "\x00" + settings.WarningMessageID))
	return hex.EncodeToString(sum[:])
}

// ReserveWarningSend takes the fence before a replacement warning is posted.
// It fails with ErrWarningDeliveryUnknown if an earlier post for the same
// channel and warning may have gone through. Callers hold the guild's
// warning lock.
func (s *Service) ReserveWarningSend(ctx context.Context, guildID string, settings Settings) error {
	db := s.store.db.WithContext(ctx)
	row := WarningRefresh{GuildID: guildID, Revision: ulid.Make().String(), NextAttemptAt: time.Now().UTC()}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return err
	}
	identity := warningSendIdentity(settings)
	result := db.Model(&WarningRefresh{}).
		Where("guild_id = ? AND send_identity <> ?", guildID, identity).
		Update("send_identity", identity)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrWarningDeliveryUnknown
	}
	return nil
}

// ReleaseWarningSend lifts the fence after Discord definitely refused the
// post. An uncertain result leaves it held.
func (s *Service) ReleaseWarningSend(ctx context.Context, guildID string, settings Settings) error {
	return s.store.db.WithContext(ctx).Model(&WarningRefresh{}).
		Where("guild_id = ? AND send_identity = ?", guildID, warningSendIdentity(settings)).
		Update("send_identity", "").Error
}

// RecordWarningReplacement stores the ID of a replacement warning, but only
// while the settings it was made for are still current, so it never
// overwrites an admin's concurrent edit. It writes no audit entry: it is
// upkeep, not a settings change.
func (s *Service) RecordWarningReplacement(ctx context.Context, guildID string, previous Settings, messageID string) error {
	if messageID == "" {
		return errors.New("warning message ID is required")
	}
	return s.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("guild_id = ? AND module_id = ?", guildID, modules.Honeypots).
			First(&config).Error
		if err != nil {
			return err
		}
		var current Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &current); err != nil {
			return err
		}
		if !config.Enabled || current.ChannelDiscordID != previous.ChannelDiscordID ||
			current.WarningMessageID != previous.WarningMessageID || current.WarningText != previous.WarningText {
			return errors.New("honeypot warning configuration changed")
		}
		current.WarningMessageID = messageID
		encoded, err := json.Marshal(current)
		if err != nil {
			return err
		}
		return tx.Model(&config).Update("config_json", string(encoded)).Error
	})
}
