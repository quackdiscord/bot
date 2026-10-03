package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CreateAuditLogEntry creates audit log entry while preserving validation, authorization, and persistence invariants.
func (s *Store) CreateAuditLogEntry(ctx context.Context, entry *quack.AuditLogEntry) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	return createAuditLogEntry(s.db.WithContext(ctx), entry, time.Now().UTC())
}

// ListAuditLogEntries returns audit log entries subject to authorization, ordering, and filtering constraints.
func (s *Store) ListAuditLogEntries(ctx context.Context, guildID string) ([]quack.AuditLogEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var entries []quack.AuditLogEntry
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Order("created_at ASC").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list audit log entries: %w", err)
	}

	return entries, nil
}

// ListAuditLogEntriesFiltered returns audit log entries filtered subject to authorization, ordering, and filtering constraints.
func (s *Store) ListAuditLogEntriesFiltered(ctx context.Context, params quack.ListAuditLogEntriesParams) (*quack.ListAuditLogEntriesResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := filteredAuditQuery(s.db.WithContext(ctx).Model(&quack.AuditLogEntry{}), params).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count audit log entries: %w", err)
	}

	query := filteredAuditQuery(s.db.WithContext(ctx).Model(&quack.AuditLogEntry{}), params)
	if params.BeforeID != "" {
		var cursor quack.AuditLogEntry
		result := s.db.WithContext(ctx).Where("guild_id = ? AND id = ?", params.GuildID, params.BeforeID).First(&cursor)
		if result.Error != nil {
			return nil, fmt.Errorf("resolve audit cursor: %w", result.Error)
		}
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var entries []quack.AuditLogEntry
	if err := query.
		Order("created_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list filtered audit log entries: %w", err)
	}

	return &quack.ListAuditLogEntriesResult{Entries: entries, Total: total}, nil
}

// filteredAuditQuery encapsulates the filtered audit query rule so callers share one consistent package implementation.
func filteredAuditQuery(query *gorm.DB, params quack.ListAuditLogEntriesParams) *gorm.DB {
	query = query.Where("guild_id = ?", params.GuildID)
	if params.ActorDiscordUserID != "" {
		query = query.Where("actor_discord_user_id = ?", params.ActorDiscordUserID)
	}
	if params.Source != "" {
		query = query.Where("source = ?", params.Source)
	}
	if params.Action != "" {
		query = query.Where("action = ?", params.Action)
	}
	if params.ResourceType != "" {
		query = query.Where("resource_type = ?", params.ResourceType)
	}
	if params.ResourceID != "" {
		query = query.Where("resource_id = ?", params.ResourceID)
	}
	if params.Result != "" {
		query = query.Where("result = ?", params.Result)
	}
	if params.CaseID != "" {
		pattern := `%"case_id":"` + params.CaseID + `"%`
		query = query.Where("(resource_type = ? AND resource_id = ?) OR metadata_json LIKE ?", "case", params.CaseID, pattern)
	}
	if params.MemberDiscordUserID != "" {
		pattern := `%"member_discord_user_id":"` + params.MemberDiscordUserID + `"%`
		targetPattern := `%"target_discord_user_id":"` + params.MemberDiscordUserID + `"%`
		query = query.Where("actor_discord_user_id = ? OR metadata_json LIKE ? OR metadata_json LIKE ?", params.MemberDiscordUserID, pattern, targetPattern)
	}
	if params.CreatedAfter != "" {
		if value, err := time.Parse(time.RFC3339Nano, params.CreatedAfter); err == nil {
			query = query.Where("created_at >= ?", value.UTC())
		}
	}
	if params.CreatedBefore != "" {
		if value, err := time.Parse(time.RFC3339Nano, params.CreatedBefore); err == nil {
			query = query.Where("created_at < ?", value.UTC())
		}
	}
	return query
}

// createAuditLogEntry creates audit log entry while preserving validation, authorization, and persistence invariants.
func createAuditLogEntry(db *gorm.DB, entry *quack.AuditLogEntry, now time.Time) error {
	if entry == nil {
		return nil
	}
	if entry.ResourceID == "" {
		entry.ResourceID = "unknown"
	}
	if entry.MetadataJSON == "" {
		entry.MetadataJSON = "{}"
	}
	entry.MetadataJSON = quack.RedactAuditMetadata(entry.MetadataJSON)
	entry.FailureReason = redactAuditFailureReason(entry.FailureReason)
	if err := prepareULIDModel(&entry.ULIDModel, now); err != nil {
		return fmt.Errorf("prepare audit log entry model: %w", err)
	}
	if err := db.Create(entry).Error; err != nil {
		return fmt.Errorf("create audit log entry: %w", err)
	}

	return nil
}

// redactAuditFailureReason keeps bounded classifications while stripping credentials accidentally embedded in an error.
func redactAuditFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	for _, fragment := range []string{"token=", "authorization:", "bearer ", "password=", "secret=", "cookie="} {
		if strings.Contains(lower, fragment) {
			return "sensitive failure detail redacted"
		}
	}
	runes := []rune(value)
	if len(runes) > 240 {
		return string(runes[:240])
	}
	return value
}
