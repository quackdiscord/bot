package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/structs"
)

type UpsertGuildParams struct {
	DiscordGuildID     string
	Name               string
	IconURL            string
	OwnerDiscordUserID string
}

type UpsertStaffMemberParams struct {
	GuildID                string
	DiscordUserID          string
	LastSeenPermissionBits uint64
	LastKnownDisplayName   string
	LastActiveAt           time.Time
}

func (s *Store) GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*structs.Guild, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var guild structs.Guild
	result := s.db.WithContext(ctx).Where("discord_guild_id = ?", discordGuildID).Limit(1).Find(&guild)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild by discord id: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}

	return &guild, nil
}

func (s *Store) UpsertGuild(ctx context.Context, params UpsertGuildParams) (*structs.Guild, error) {
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

	guild := &structs.Guild{
		DiscordGuildID:     params.DiscordGuildID,
		Name:               params.Name,
		IconURL:            params.IconURL,
		OwnerDiscordUserID: params.OwnerDiscordUserID,
		RolloutState:       structs.GuildRolloutDisabled,
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

func (s *Store) EnsureGuildSettings(ctx context.Context, guildID string) (*structs.GuildSettings, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var settings structs.GuildSettings
	result := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Limit(1).Find(&settings)
	if result.Error != nil {
		return nil, fmt.Errorf("get guild settings: %w", result.Error)
	}
	if result.RowsAffected > 0 {
		return &settings, nil
	}

	now := time.Now().UTC()
	settings = structs.GuildSettings{
		GuildID:                   guildID,
		FeatureFlagsJSON:          "{}",
		GuildModerationConfigJSON: "{}",
	}
	if err := prepareULIDModel(&settings.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare guild settings model: %w", err)
	}

	if err := s.db.WithContext(ctx).Create(&settings).Error; err != nil {
		return nil, fmt.Errorf("create guild settings: %w", err)
	}

	return &settings, nil
}

func (s *Store) UpsertStaffMember(ctx context.Context, params UpsertStaffMemberParams) (*structs.StaffMember, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	activeAt := params.LastActiveAt
	if activeAt.IsZero() {
		activeAt = now
	}

	var staff structs.StaffMember
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

	staff = structs.StaffMember{
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

func (s *Store) GetStaffMember(ctx context.Context, guildID, discordUserID string) (*structs.StaffMember, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var staff structs.StaffMember
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

func (s *Store) EnsureDefaultGuildPermissionPolicies(ctx context.Context, guildID string) ([]structs.GuildPermissionPolicy, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	defaults := DefaultGuildPermissionPolicies(guildID)
	var existing []structs.GuildPermissionPolicy
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("list guild permission policies: %w", err)
	}

	existingByAction := make(map[structs.PermissionAction]struct{}, len(existing))
	for _, policy := range existing {
		existingByAction[policy.Action] = struct{}{}
	}

	now := time.Now().UTC()
	missing := make([]structs.GuildPermissionPolicy, 0, len(defaults))
	for _, policy := range defaults {
		if _, ok := existingByAction[policy.Action]; ok {
			continue
		}

		policy := policy
		if err := prepareULIDModel(&policy.ULIDModel, now); err != nil {
			return nil, fmt.Errorf("prepare guild permission policy model: %w", err)
		}
		missing = append(missing, policy)
	}

	if len(missing) > 0 {
		if err := s.db.WithContext(ctx).Create(&missing).Error; err != nil {
			return nil, fmt.Errorf("create guild permission policies: %w", err)
		}
		existing = append(existing, missing...)
	}

	return existing, nil
}

func (s *Store) ListGuildPermissionPolicies(ctx context.Context, guildID string) ([]structs.GuildPermissionPolicy, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var policies []structs.GuildPermissionPolicy
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Find(&policies).Error; err != nil {
		return nil, fmt.Errorf("list guild permission policies: %w", err)
	}

	return policies, nil
}

func DefaultGuildPermissionPolicies(guildID string) []structs.GuildPermissionPolicy {
	moderateMembers := uint64(discordgo.PermissionModerateMembers)
	manageGuild := uint64(discordgo.PermissionManageGuild)

	return []structs.GuildPermissionPolicy{
		{GuildID: guildID, Action: structs.PermissionActionCaseCreate, MinimumPermissionBits: moderateMembers, Description: "Create moderation cases"},
		{GuildID: guildID, Action: structs.PermissionActionCaseTemplateRead, MinimumPermissionBits: moderateMembers, Description: "Read moderation case templates"},
		{GuildID: guildID, Action: structs.PermissionActionCaseTemplateWrite, MinimumPermissionBits: manageGuild, Description: "Create and update moderation case templates"},
		{GuildID: guildID, Action: structs.PermissionActionCaseTemplateDelete, MinimumPermissionBits: manageGuild, Description: "Archive or delete moderation case templates"},
		{GuildID: guildID, Action: structs.PermissionActionAppealReview, MinimumPermissionBits: moderateMembers, Description: "Review moderation appeals"},
		{GuildID: guildID, Action: structs.PermissionActionTicketResolve, MinimumPermissionBits: moderateMembers, Description: "Resolve moderation tickets"},
		{GuildID: guildID, Action: structs.PermissionActionAuditRead, MinimumPermissionBits: manageGuild, Description: "Read audit logs"},
	}
}
