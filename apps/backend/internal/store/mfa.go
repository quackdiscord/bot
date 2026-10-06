package store

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm/clause"
)

// DiscordUserMFAEnabled reports whether the user had two-factor
// authentication on when they last signed in to the dashboard. A user who
// never signed in reports false.
func (s *Store) DiscordUserMFAEnabled(ctx context.Context, discordUserID string) (bool, error) {
	var record discordUserMFARecord
	found, err := first(s.db.WithContext(ctx).Where("discord_user_id = ?", discordUserID), &record)
	if err != nil {
		return false, fmt.Errorf("get discord user mfa: %w", err)
	}
	return found && record.MFAEnabled, nil
}

// RecordDiscordUserMFA saves what Discord just reported about the user's
// two-factor authentication, replacing what Quack knew before.
func (s *Store) RecordDiscordUserMFA(ctx context.Context, discordUserID string, enabled bool, checkedAt time.Time) error {
	checkedAt = checkedAt.UTC()
	record := discordUserMFARecord{
		DiscordUserID: discordUserID,
		CreatedAt:     checkedAt,
		UpdatedAt:     checkedAt,
		MFAEnabled:    enabled,
		MFACheckedAt:  checkedAt,
	}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "discord_user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"mfa_enabled", "mfa_checked_at", "updated_at"}),
	}).Create(&record).Error
	if err != nil {
		return fmt.Errorf("record discord user mfa: %w", err)
	}
	return nil
}
