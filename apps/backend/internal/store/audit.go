package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// ErrAuditImmutable is returned for any attempt to update or delete an audit
// entry through GORM.
var ErrAuditImmutable = errors.New("audit entries are append-only")

// CreateAuditLogEntry appends entry and fills in its ID and timestamps.
func (s *Store) CreateAuditLogEntry(ctx context.Context, entry *quack.AuditLogEntry) error {
	return createAuditLogEntry(s.db.WithContext(ctx), entry, time.Now().UTC())
}

// ListAuditLogEntries returns a guild's whole audit log, oldest first.
func (s *Store) ListAuditLogEntries(ctx context.Context, guildID string) ([]quack.AuditLogEntry, error) {
	var records []auditRecord
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).
		Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list audit log entries: %w", err)
	}
	return modelsOf(records, auditRecord.model), nil
}

// ListAuditLogEntriesFiltered returns a page of a guild's audit log, newest
// first. BeforeID pages by cursor; Offset pages by position.
func (s *Store) ListAuditLogEntriesFiltered(ctx context.Context, params quack.ListAuditLogEntriesParams) (*quack.ListAuditLogEntriesResult, error) {
	db := s.db.WithContext(ctx)
	limit, offset := page(params.Limit, params.Offset)
	var total int64
	if err := filterAudit(db.Model(&auditRecord{}), params).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count audit log entries: %w", err)
	}
	query := filterAudit(db.Model(&auditRecord{}), params)
	if params.BeforeID != "" {
		var cursor auditRecord
		found, err := first(db.Where("guild_id = ? AND id = ?", params.GuildID, params.BeforeID), &cursor)
		if err != nil {
			return nil, fmt.Errorf("resolve audit cursor: %w", err)
		}
		if !found {
			return nil, errors.New("resolve audit cursor: entry not found")
		}
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	var records []auditRecord
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list audit log entries: %w", err)
	}
	return &quack.ListAuditLogEntriesResult{Entries: modelsOf(records, auditRecord.model), Total: total}, nil
}

// filterAudit applies the non-empty filters in params. Case and member
// filters also match entries that only mention them in their metadata.
func filterAudit(query *gorm.DB, params quack.ListAuditLogEntriesParams) *gorm.DB {
	query = query.Where("guild_id = ?", params.GuildID)
	for _, filter := range [][2]string{
		{"actor_discord_user_id", params.ActorDiscordUserID},
		{"source", params.Source},
		{"action", params.Action},
		{"resource_type", params.ResourceType},
		{"resource_id", params.ResourceID},
		{"result", string(params.Result)},
	} {
		if filter[1] != "" {
			query = query.Where(filter[0]+" = ?", filter[1])
		}
	}
	if id := params.CaseID; id != "" {
		caseMatch, caseArgs := metadataMatch("case_id", id)
		query = query.Where("(resource_type = ? AND resource_id = ?) OR "+caseMatch,
			append([]any{"case", id}, caseArgs...)...)
	}
	if id := params.MemberDiscordUserID; id != "" {
		memberMatch, memberArgs := metadataMatch("member_discord_user_id", id)
		targetMatch, targetArgs := metadataMatch("target_discord_user_id", id)
		args := append(append([]any{id}, memberArgs...), targetArgs...)
		query = query.Where("actor_discord_user_id = ? OR "+memberMatch+" OR "+targetMatch, args...)
	}
	if value, err := time.Parse(time.RFC3339Nano, params.CreatedAfter); err == nil {
		query = query.Where("created_at >= ?", value.UTC())
	}
	if value, err := time.Parse(time.RFC3339Nano, params.CreatedBefore); err == nil {
		query = query.Where("created_at < ?", value.UTC())
	}
	return query
}

// likeEscaper escapes LIKE's wildcards with '!', which the filters name in
// an ESCAPE clause. A backslash would need different quoting on MySQL and
// SQLite; '!' means the same on both.
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

// metadataMatch returns a SQL condition and its arguments matching metadata
// JSON that contains "key":"value". MySQL stores json columns normalized as
// "key": "value" while SQLite keeps the text as written, so both spellings
// are tried. value is matched literally; % and _ cannot widen the filter.
func metadataMatch(key, value string) (string, []any) {
	escaped := likeEscaper.Replace(value)
	return "(metadata_json LIKE ? ESCAPE '!' OR metadata_json LIKE ? ESCAPE '!')", []any{
		`%"` + key + `":"` + escaped + `"%`,
		`%"` + key + `": "` + escaped + `"%`,
	}
}

// createAuditLogEntry appends entry inside db, which may be a transaction. It
// redacts the metadata and failure reason and fills in the entry's ID and
// timestamps. An important entry (quack.IsImportantAuditAction) also queues
// its audit mirror delivery, committed together with it.
func createAuditLogEntry(db *gorm.DB, entry *quack.AuditLogEntry, now time.Time) error {
	if entry == nil {
		return nil
	}
	entry.ResourceID = cmp.Or(entry.ResourceID, "unknown")
	entry.MetadataJSON = quack.RedactAuditMetadata(cmp.Or(entry.MetadataJSON, "{}"))
	entry.FailureReason = redactFailureReason(entry.FailureReason)
	stamp(&entry.ULIDModel, now)
	record := newAuditRecord(*entry)
	if !quack.IsImportantAuditAction(record.Action) {
		if err := db.Create(&record).Error; err != nil {
			return fmt.Errorf("create audit log entry: %w", err)
		}
		return nil
	}
	// Inside a caller's transaction this is a savepoint.
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("create audit log entry: %w", err)
		}
		return queueAuditMirrorDelivery(tx, record, now)
	})
}

// writeAudit appends a copy of entry about resourceID. A nil entry is a
// no-op, for callers whose audit is optional.
func writeAudit(tx *gorm.DB, entry *quack.AuditLogEntry, resourceID string, now time.Time) error {
	if entry == nil {
		return nil
	}
	copied := *entry
	copied.ResourceID = resourceID
	return createAuditLogEntry(tx, &copied, now)
}

// redactFailureReason keeps short error classifications but drops anything
// that looks like it carries a credential.
func redactFailureReason(value string) string {
	value = strings.TrimSpace(value)
	lower := strings.ToLower(value)
	for _, fragment := range []string{"token=", "authorization:", "bearer ", "password=", "secret=", "cookie="} {
		if strings.Contains(lower, fragment) {
			return "sensitive failure detail redacted"
		}
	}
	if runes := []rune(value); len(runes) > 240 {
		return string(runes[:240])
	}
	return value
}

// auditCallbackMu serializes callback registration; tests build many stores
// on shared databases.
var auditCallbackMu sync.Mutex

// installAuditImmutability makes GORM refuse updates and deletes against the
// audit table, so no code path can rewrite history.
func installAuditImmutability(db *gorm.DB) {
	auditCallbackMu.Lock()
	defer auditCallbackMu.Unlock()
	const name = "quack:audit_append_only"
	if db.Callback().Update().Get(name) == nil {
		_ = db.Callback().Update().Before("gorm:update").Register(name, rejectAuditMutation)
	}
	if db.Callback().Delete().Get(name) == nil {
		_ = db.Callback().Delete().Before("gorm:delete").Register(name, rejectAuditMutation)
	}
}

// rejectAuditMutation fails the statement when it targets the audit table.
func rejectAuditMutation(db *gorm.DB) {
	if db.Statement != nil && db.Statement.Table == "audit_log_entries" {
		_ = db.AddError(ErrAuditImmutable)
	}
}

func newAuditRecord(e quack.AuditLogEntry) auditRecord {
	return auditRecord{
		ID:                  e.ID,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
		GuildID:             e.GuildID,
		ActorDiscordUserID:  e.ActorDiscordUserID,
		ActorPermissionBits: e.ActorPermissionBits,
		Source:              e.Source,
		Action:              e.Action,
		ResourceType:        e.ResourceType,
		ResourceID:          e.ResourceID,
		Result:              e.Result,
		FailureReason:       e.FailureReason,
		CorrelationID:       e.CorrelationID,
		RequestID:           e.RequestID,
		MetadataJSON:        e.MetadataJSON,
	}
}

func (r auditRecord) model() quack.AuditLogEntry {
	return quack.AuditLogEntry{
		ULIDModel:           ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:             r.GuildID,
		ActorDiscordUserID:  r.ActorDiscordUserID,
		ActorPermissionBits: r.ActorPermissionBits,
		Source:              r.Source,
		Action:              r.Action,
		ResourceType:        r.ResourceType,
		ResourceID:          r.ResourceID,
		Result:              r.Result,
		FailureReason:       r.FailureReason,
		CorrelationID:       r.CorrelationID,
		RequestID:           r.RequestID,
		MetadataJSON:        r.MetadataJSON,
	}
}
