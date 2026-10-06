package store

import (
	"context"
	"fmt"
	"strings"
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
		r.ModeratorRoleIDs = joinRoleIDs(in.ModeratorRoleIDs)
		r.RulesManagerRoleIDs = joinRoleIDs(in.RulesManagerRoleIDs)
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
		ModeratorRoleIDs:                  splitRoleIDs(r.ModeratorRoleIDs),
		RulesManagerRoleIDs:               splitRoleIDs(r.RulesManagerRoleIDs),
	}
}

// ListGuildStaffRoles returns the staff roles of the guilds among
// discordGuildIDs that have settings, keyed by Discord guild ID, in one
// query.
func (s *Store) ListGuildStaffRoles(ctx context.Context, discordGuildIDs []string) (map[string]quack.StaffRoles, error) {
	out := make(map[string]quack.StaffRoles, len(discordGuildIDs))
	if len(discordGuildIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		DiscordGuildID      string
		ModeratorRoleIDs    string
		RulesManagerRoleIDs string
	}
	err := s.db.WithContext(ctx).Table("guild_settings").
		Select("guilds.discord_guild_id, guild_settings.moderator_role_ids, guild_settings.rules_manager_role_ids").
		Joins("JOIN guilds ON guilds.id = guild_settings.guild_id").
		Where("guilds.discord_guild_id IN ?", discordGuildIDs).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list guild staff roles: %w", err)
	}
	for _, row := range rows {
		out[row.DiscordGuildID] = quack.StaffRoles{
			ModeratorRoleIDs:    splitRoleIDs(row.ModeratorRoleIDs),
			RulesManagerRoleIDs: splitRoleIDs(row.RulesManagerRoleIDs),
		}
	}
	return out, nil
}

// joinRoleIDs stores a role ID list as one comma-separated column. Role IDs
// are decimal snowflakes, so they never contain a comma.
func joinRoleIDs(roleIDs []string) string {
	return strings.Join(roleIDs, ",")
}

// splitRoleIDs reads a column written by joinRoleIDs.
func splitRoleIDs(column string) []string {
	if column == "" {
		return []string{}
	}
	return strings.Split(column, ",")
}
