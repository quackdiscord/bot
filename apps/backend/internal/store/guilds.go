package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// GetGuildByDiscordID retrieves guild by discord id without exposing the underlying adapter implementation.
func (s *Store) GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var guild quack.Guild
	result := s.db.WithContext(ctx).Where("discord_guild_id = ?", discordGuildID).Limit(1).Find(&guild)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild by discord id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	return &guild, nil
}

// GetGuildByID retrieves the durable guild identity used by case notifications.
func (s *Store) GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var guild quack.Guild
	result := s.db.WithContext(ctx).Where("id = ?", guildID).Limit(1).Find(&guild)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild by id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &guild, nil
}

// UpsertGuild encapsulates the upsert guild rule so callers share one consistent package implementation.
func (s *Store) UpsertGuild(ctx context.Context, params quack.UpsertGuildParams) (*quack.Guild, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	existing, err := s.GetGuildByDiscordID(ctx, params.DiscordGuildID)
	if err != nil {
		return nil, err
	}

	if existing != nil {
		if existing.Name == params.Name &&
			existing.IconURL == params.IconURL &&
			existing.OwnerDiscordUserID == params.OwnerDiscordUserID &&
			existing.IsActive {
			return existing, nil
		}

		existing.Name = params.Name
		existing.IconURL = params.IconURL
		existing.OwnerDiscordUserID = params.OwnerDiscordUserID
		existing.IsActive = true
		existing.UpdatedAt = now

		if err := s.db.WithContext(ctx).Save(existing).Error; err != nil {
			return nil, fmt.Errorf("update guild: %w", err)
		}
		return existing, nil
	}

	guild := &quack.Guild{
		DiscordGuildID:     params.DiscordGuildID,
		Name:               params.Name,
		IconURL:            params.IconURL,
		OwnerDiscordUserID: params.OwnerDiscordUserID,
		IsActive:           true,
	}
	if err := prepareULIDModel(&guild.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare guild model: %w", err)
	}

	if err := s.db.WithContext(ctx).Create(guild).Error; err != nil {
		return nil, fmt.Errorf("create guild: %w", err)
	}

	return guild, nil
}

// UpsertStaffMember encapsulates the upsert staff member rule so callers share one consistent package implementation.
func (s *Store) UpsertStaffMember(ctx context.Context, params quack.UpsertStaffMemberParams) (*quack.StaffMember, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	activeAt := params.LastActiveAt
	if activeAt.IsZero() {
		activeAt = now
	}

	var staff quack.StaffMember
	result := s.db.WithContext(ctx).
		Where("guild_id = ? AND discord_user_id = ?", params.GuildID, params.DiscordUserID).
		Limit(1).
		Find(&staff)
	if result.Error != nil {
		return nil, fmt.Errorf("get staff member: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		shouldUpdateActivity := staff.LastActiveAt == nil || activeAt.Sub(*staff.LastActiveAt) >= 5*time.Minute
		shouldUpdate := staff.LastSeenPermissionBits != params.LastSeenPermissionBits ||
			staff.LastKnownDisplayName != params.LastKnownDisplayName ||
			shouldUpdateActivity
		if !shouldUpdate {
			return &staff, nil
		}

		staff.LastSeenPermissionBits = params.LastSeenPermissionBits
		staff.LastKnownDisplayName = params.LastKnownDisplayName
		if shouldUpdateActivity {
			staff.LastActiveAt = &activeAt
		}
		staff.UpdatedAt = now

		if err := s.db.WithContext(ctx).Save(&staff).Error; err != nil {
			return nil, fmt.Errorf("update staff member: %w", err)
		}
		return &staff, nil
	}

	staff = quack.StaffMember{
		GuildID:                params.GuildID,
		DiscordUserID:          params.DiscordUserID,
		LastSeenPermissionBits: params.LastSeenPermissionBits,
		LastKnownDisplayName:   params.LastKnownDisplayName,
		LastActiveAt:           &activeAt,
	}
	if err := prepareULIDModel(&staff.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare staff member model: %w", err)
	}

	if err := s.db.WithContext(ctx).Create(&staff).Error; err != nil {
		return nil, fmt.Errorf("create staff member: %w", err)
	}

	return &staff, nil
}

// GetStaffMember retrieves staff member without exposing the underlying adapter implementation.
func (s *Store) GetStaffMember(ctx context.Context, guildID, discordUserID string) (*quack.StaffMember, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var staff quack.StaffMember
	result := s.db.WithContext(ctx).
		Where("guild_id = ? AND discord_user_id = ?", guildID, discordUserID).
		Limit(1).
		Find(&staff)
	if result.Error != nil {
		return nil, fmt.Errorf("get staff member: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	return &staff, nil
}
