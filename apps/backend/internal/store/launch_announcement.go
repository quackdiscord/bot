package store

import (
	"context"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// ListLaunchAnnouncementTargets returns up to limit active guilds whose v5
// announcement has not been claimed, oldest install first.
func (s *Store) ListLaunchAnnouncementTargets(ctx context.Context, limit int) ([]quack.LaunchAnnouncementTarget, error) {
	var rows []struct {
		GuildID                     string
		DiscordGuildID              string
		OwnerDiscordUserID          string
		AuditMirrorChannelDiscordID string
	}
	err := s.db.WithContext(ctx).
		Table("guilds").
		Select("guilds.id AS guild_id, guilds.discord_guild_id, guilds.owner_discord_user_id, guild_settings.audit_mirror_channel_discord_id").
		Joins("JOIN guild_settings ON guild_settings.guild_id = guilds.id").
		Where("guilds.is_active = ? AND guild_settings.launch_announced_at IS NULL", true).
		Order("guilds.created_at, guilds.id").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list launch announcement targets: %w", err)
	}
	targets := make([]quack.LaunchAnnouncementTarget, len(rows))
	for i, row := range rows {
		targets[i] = quack.LaunchAnnouncementTarget(row)
	}
	return targets, nil
}

// ClaimLaunchAnnouncement sets the guild's launch_announced_at only while it
// is still empty, so exactly one caller wins.
func (s *Store) ClaimLaunchAnnouncement(ctx context.Context, guildID string, now time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Model(&guildSettingsRecord{}).
		Where("guild_id = ? AND launch_announced_at IS NULL", guildID).
		Update("launch_announced_at", now.UTC())
	if result.Error != nil {
		return false, fmt.Errorf("claim launch announcement: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// ReleaseLaunchAnnouncement clears the guild's claim.
func (s *Store) ReleaseLaunchAnnouncement(ctx context.Context, guildID string) error {
	err := s.db.WithContext(ctx).Model(&guildSettingsRecord{}).
		Where("guild_id = ?", guildID).
		Update("launch_announced_at", nil).Error
	if err != nil {
		return fmt.Errorf("release launch announcement: %w", err)
	}
	return nil
}
