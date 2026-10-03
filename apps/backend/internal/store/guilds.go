package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// staffActivityInterval is how stale a staff member's last-active time may
// get before a request refreshes it, so busy moderators don't write on every
// request.
const staffActivityInterval = 5 * time.Minute

// GetGuildByDiscordID returns the guild with a Discord ID, or nil.
func (s *Store) GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error) {
	query := s.db.WithContext(ctx).Where("discord_guild_id = ?", discordGuildID)
	return findOne(query, "get guild", guildRecord.model)
}

// GetGuildByID returns the guild with an internal ID, or nil.
func (s *Store) GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error) {
	return findOne(s.db.WithContext(ctx).Where("id = ?", guildID), "get guild", guildRecord.model)
}

// UpsertGuild refreshes a guild's Discord metadata and marks it active,
// creating it if needed. Unchanged guilds are not written.
func (s *Store) UpsertGuild(ctx context.Context, params quack.UpsertGuildParams) (*quack.Guild, error) {
	db := s.db.WithContext(ctx)
	now := time.Now().UTC()
	var record guildRecord
	found, err := first(db.Where("discord_guild_id = ?", params.DiscordGuildID), &record)
	if err != nil {
		return nil, fmt.Errorf("get guild: %w", err)
	}
	if found && record.Name == params.Name && record.IconURL == params.IconURL &&
		record.OwnerDiscordUserID == params.OwnerDiscordUserID && record.IsActive {
		guild := record.model()
		return &guild, nil
	}
	if !found {
		record = guildRecord{ID: quack.NewID(), CreatedAt: now, DiscordGuildID: params.DiscordGuildID}
	}
	record.refresh(params.Name, params.IconURL, params.OwnerDiscordUserID, now)
	if err := db.Save(&record).Error; err != nil {
		return nil, fmt.Errorf("save guild: %w", err)
	}
	guild := record.model()
	return &guild, nil
}

// GetStaffMember returns a staff member's cached attribution, or nil.
func (s *Store) GetStaffMember(ctx context.Context, guildID, discordUserID string) (*quack.StaffMember, error) {
	query := s.db.WithContext(ctx).Where("guild_id = ? AND discord_user_id = ?", guildID, discordUserID)
	return findOne(query, "get staff member", staffMemberRecord.model)
}

// UpsertStaffMember refreshes a staff member's cached permissions, display
// name, and activity.
func (s *Store) UpsertStaffMember(ctx context.Context, params quack.UpsertStaffMemberParams) (*quack.StaffMember, error) {
	db := s.db.WithContext(ctx)
	now := time.Now().UTC()
	activeAt := params.LastActiveAt
	if activeAt.IsZero() {
		activeAt = now
	}
	var record staffMemberRecord
	query := db.Where("guild_id = ? AND discord_user_id = ?", params.GuildID, params.DiscordUserID)
	found, err := first(query, &record)
	if err != nil {
		return nil, fmt.Errorf("get staff member: %w", err)
	}
	refreshActivity := record.LastActiveAt == nil || activeAt.Sub(*record.LastActiveAt) >= staffActivityInterval
	if found && !refreshActivity && record.LastSeenPermissionBits == params.LastSeenPermissionBits &&
		record.LastKnownDisplayName == params.LastKnownDisplayName {
		staff := record.model()
		return &staff, nil
	}
	if !found {
		record = staffMemberRecord{
			ID:            quack.NewID(),
			CreatedAt:     now,
			GuildID:       params.GuildID,
			DiscordUserID: params.DiscordUserID,
		}
	}
	record.LastSeenPermissionBits = params.LastSeenPermissionBits
	record.LastKnownDisplayName = params.LastKnownDisplayName
	if refreshActivity {
		record.LastActiveAt = &activeAt
	}
	record.UpdatedAt = now
	if err := db.Save(&record).Error; err != nil {
		return nil, fmt.Errorf("save staff member: %w", err)
	}
	staff := record.model()
	return &staff, nil
}

// BootstrapGuild installs or reactivates a guild in one transaction: it
// refreshes the guild, creates its settings, creates the starter template
// once, and clears configured channels that no longer exist.
func (s *Store) BootstrapGuild(ctx context.Context, params quack.BootstrapGuildParams) (*quack.BootstrapGuildResult, error) {
	result := &quack.BootstrapGuildResult{}
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild guildRecord
		found, err := first(forUpdate(tx).Where("discord_guild_id = ?", params.DiscordGuildID), &guild)
		if err != nil {
			return fmt.Errorf("get guild for bootstrap: %w", err)
		}
		wasActive := found && guild.IsActive
		if !found {
			guild = guildRecord{ID: quack.NewID(), CreatedAt: now, DiscordGuildID: params.DiscordGuildID}
			result.GuildCreated = true
		}
		guild.refresh(params.Name, params.IconURL, params.OwnerDiscordUserID, now)
		if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("save bootstrap guild: %w", err)
		}

		var settings guildSettingsRecord
		found, err = first(forUpdate(tx).Where("guild_id = ?", guild.ID), &settings)
		if err != nil {
			return fmt.Errorf("get bootstrap settings: %w", err)
		}
		if !found {
			settings = guildSettingsRecord{
				ID:                         quack.NewID(),
				CreatedAt:                  now,
				GuildID:                    guild.ID,
				StarterPolicyNoticePending: true,
			}
		}

		if settings.StarterPolicyTemplateID == "" {
			starter, created, err := ensureStarterPolicy(tx, guild.ID, params.Starter, now)
			if err != nil {
				return err
			}
			settings.StarterPolicyTemplateID = starter.Template.ID
			settings.StarterPolicyNoticePending = true
			result.StarterTemplate = *starter
			result.StarterTemplateCreated = created
			if created {
				err := systemAudit(tx, guild.ID, "case_template.bootstrap", "case_template", starter.Template.ID, now)
				if err != nil {
					return err
				}
			}
		} else {
			starter, err := expandTemplate(tx, guild.ID, settings.StarterPolicyTemplateID)
			if err != nil {
				return err
			}
			if starter == nil {
				return errors.New("configured starter policy template is missing")
			}
			result.StarterTemplate = *starter
		}

		repaired := false
		if params.KnownChannelDiscordIDs != nil {
			known := make(map[string]bool, len(params.KnownChannelDiscordIDs))
			for _, id := range params.KnownChannelDiscordIDs {
				known[id] = true
			}
			for _, channel := range []*string{&settings.AuditMirrorChannelDiscordID, &settings.ManagedEvidenceChannelDiscordID} {
				if *channel != "" && !known[*channel] {
					*channel = ""
					repaired = true
				}
			}
		}
		settings.UpdatedAt = now
		if err := tx.Save(&settings).Error; err != nil {
			return fmt.Errorf("save bootstrap settings: %w", err)
		}
		if repaired {
			err := systemAudit(tx, guild.ID, "guild_settings.channel_references.repaired",
				"guild_settings", settings.ID, now)
			if err != nil {
				return err
			}
		}
		if !wasActive {
			if err := systemAudit(tx, guild.ID, "guild.lifecycle.bootstrap", "guild", guild.ID, now); err != nil {
				return err
			}
		}
		result.Guild = guild.model()
		result.Settings = settings.model()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ensureStarterPolicy saves starter as the guild's starter template, or
// returns the one a previous bootstrap saved.
func ensureStarterPolicy(tx *gorm.DB, guildID string, starter quack.ExpandedCaseTemplate, now time.Time) (*quack.ExpandedCaseTemplate, bool, error) {
	if starter.Template.Slug == "" {
		return nil, false, errors.New("starter policy is required")
	}
	var existing templateRecord
	found, err := first(tx.Where("guild_id = ? AND slug = ?", guildID, starter.Template.Slug), &existing)
	if err != nil {
		return nil, false, fmt.Errorf("find starter policy: %w", err)
	}
	if found {
		expanded, err := expandTemplate(tx, guildID, existing.ID)
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
	id, err := createTemplate(tx, template, starter.ContextFields, starter.Levels, now)
	if err != nil {
		return nil, false, fmt.Errorf("create starter policy: %w", err)
	}
	expanded, err := expandTemplate(tx, guildID, id)
	return expanded, true, err
}

// DeactivateGuild marks a guild Quack has left as inactive. Its data stays.
// It returns nil when the guild is unknown.
func (s *Store) DeactivateGuild(ctx context.Context, discordGuildID string, audit *quack.AuditLogEntry) (*quack.Guild, error) {
	var guild guildRecord
	now := time.Now().UTC()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).Where("discord_guild_id = ?", discordGuildID), &guild)
		if err != nil {
			return fmt.Errorf("get guild for deactivation: %w", err)
		}
		if !found {
			return errNotFound
		}
		guild.IsActive = false
		guild.UpdatedAt = now
		if err := tx.Save(&guild).Error; err != nil {
			return fmt.Errorf("deactivate guild: %w", err)
		}
		if audit == nil {
			return nil
		}
		entry := *audit
		entry.GuildID = guild.ID
		return writeAudit(tx, &entry, guild.ID, now)
	})
	if err != nil {
		return nil, notFoundIsNil(err)
	}
	model := guild.model()
	return &model, nil
}

// systemAudit records a lifecycle change Quack made on its own.
func systemAudit(tx *gorm.DB, guildID, action, resourceType, resourceID string, now time.Time) error {
	return createAuditLogEntry(tx, &quack.AuditLogEntry{
		GuildID:            guildID,
		ActorDiscordUserID: "quack-system",
		Source:             quack.AuditSourceDiscord,
		Action:             action,
		ResourceType:       resourceType,
		ResourceID:         resourceID,
		Result:             quack.AuditResultSuccess,
	}, now)
}

// refresh copies Discord's current name, icon, and owner onto r and marks
// the guild active.
func (r *guildRecord) refresh(name, iconURL, ownerID string, now time.Time) {
	r.Name = name
	r.IconURL = iconURL
	r.OwnerDiscordUserID = ownerID
	r.IsActive = true
	r.UpdatedAt = now
}

func (r guildRecord) model() quack.Guild {
	return quack.Guild{
		ULIDModel:          ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		DiscordGuildID:     r.DiscordGuildID,
		Name:               r.Name,
		IconURL:            r.IconURL,
		OwnerDiscordUserID: r.OwnerDiscordUserID,
		IsActive:           r.IsActive,
	}
}

func (r staffMemberRecord) model() quack.StaffMember {
	return quack.StaffMember{
		ULIDModel:              ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                r.GuildID,
		DiscordUserID:          r.DiscordUserID,
		LastSeenPermissionBits: r.LastSeenPermissionBits,
		LastKnownDisplayName:   r.LastKnownDisplayName,
		LastActiveAt:           r.LastActiveAt,
	}
}
