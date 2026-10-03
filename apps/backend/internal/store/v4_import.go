package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/v4import"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// V4ImportBatchRecord is the privacy-safe durable ledger for one committed source file.
type V4ImportBatchRecord struct {
	ID                   string    `gorm:"type:varchar(64);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_v4_batch_source_checksum,priority:1;index"`
	SourceName           string    `gorm:"size:191;not null;uniqueIndex:idx_v4_batch_source_checksum,priority:2"`
	Checksum             string    `gorm:"type:char(64);not null;uniqueIndex:idx_v4_batch_source_checksum,priority:3"`
	ActorDiscordUserID   string    `gorm:"size:32;not null"`
	RecordCount          int       `gorm:"not null"`
	CreatedCount         int       `gorm:"not null"`
	AlreadyImportedCount int       `gorm:"not null"`
	WarningCount         int       `gorm:"not null"`
	CreatedAt            time.Time `gorm:"not null;index"`
}

// TableName keeps v4 import state isolated from both v4 storage and live case tables.
func (V4ImportBatchRecord) TableName() string { return "v4_import_batches" }

// V4ImportSourceRecord maps a stable v4 source identity to exactly one historical v5 case.
type V4ImportSourceRecord struct {
	ID               string    `gorm:"type:char(26);primaryKey"`
	BatchID          string    `gorm:"type:varchar(64);not null;index"`
	GuildID          string    `gorm:"type:char(26);not null;uniqueIndex:idx_v4_source_identity,priority:1;index"`
	SourceName       string    `gorm:"size:191;not null;uniqueIndex:idx_v4_source_identity,priority:2"`
	SourceID         string    `gorm:"size:191;not null;uniqueIndex:idx_v4_source_identity,priority:3"`
	SourceCaseNumber uint64    `gorm:"type:bigint unsigned;not null"`
	TargetCaseID     string    `gorm:"type:char(26);not null;uniqueIndex"`
	Fingerprint      string    `gorm:"type:char(64);not null"`
	CreatedAt        time.Time `gorm:"not null"`
}

// TableName identifies the isolated source mapping ledger.
func (V4ImportSourceRecord) TableName() string { return "v4_import_sources" }

// PreviewV4Import reports durable idempotency and number collisions without writing.
func (s *Store) PreviewV4Import(ctx context.Context, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	return inspectV4Rows(s.db.WithContext(ctx), batch, rows)
}

// ApplyV4Import atomically creates historical-only cases, source mappings, one batch ledger row, and a safe audit record.
func (s *Store) ApplyV4Import(ctx context.Context, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var decisions []v4import.Decision
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild quack.Guild
		result := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", batch.GuildID).First(&guild)
		if result.Error != nil {
			return fmt.Errorf("lock import guild: %w", result.Error)
		}
		var existingBatch V4ImportBatchRecord
		result = tx.Where("guild_id = ? AND source_name = ? AND checksum = ?", batch.GuildID, batch.SourceName, batch.Checksum).First(&existingBatch)
		if result.Error == nil {
			var inspectErr error
			decisions, inspectErr = inspectV4Rows(tx, batch, rows)
			if inspectErr != nil {
				return inspectErr
			}
			for _, decision := range decisions {
				if !decision.AlreadyImported {
					return errors.New("v4 import batch ledger is incomplete")
				}
			}
			return nil
		}
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return fmt.Errorf("inspect import batch: %w", result.Error)
		}

		var err error
		decisions, err = inspectV4Rows(tx, batch, rows)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		created, already, warnings := 0, 0, 0
		for index, row := range rows {
			decision := &decisions[index]
			warnings += len(decision.Warnings)
			if decision.AlreadyImported {
				already++
				continue
			}
			caseID := quack.NewID()
			metadata, _ := json.Marshal(map[string]any{"historical": true, "v4": map[string]any{"source_name": batch.SourceName, "source_id": row.Case.SourceID, "case_number": row.Case.CaseNumber, "action_type": row.Case.ActionType, "moderator_display_name": row.Case.ModeratorDisplayName, "target_departed": row.Case.TargetDeparted, "target_missing": row.Case.TargetMissing, "action_expires_at": row.Case.ActionExpiresAt}})
			snapshot, _ := json.Marshal(map[string]any{"historical": true, "v4_action_type": row.Case.ActionType})
			item := quack.Case{ULIDModel: quack.ULIDModel{ID: caseID, CreatedAt: row.Case.CreatedAt.UTC(), UpdatedAt: row.Case.CreatedAt.UTC()}, GuildID: batch.GuildID, CaseNumber: decision.TargetCaseNumber, TemplateVersion: 0, TemplateSnapshotJSON: string(snapshot), TargetDiscordUserID: row.Case.TargetDiscordUserID, ModeratorDiscordUserID: row.Case.ModeratorDiscordUserID, Reason: row.Case.Reason, Validity: quack.CaseValidityValid, Source: quack.CaseSourceV4Import, ContextURL: row.Case.ContextURL, MetadataJSON: string(metadata), ContextValuesJSON: "[]"}
			if err := tx.Select("*").Create(&item).Error; err != nil {
				return fmt.Errorf("create imported historical case: %w", err)
			}
			eventID := quack.NewID()
			event := quack.CaseEvent{ULIDModel: quack.ULIDModel{ID: eventID, CreatedAt: row.Case.CreatedAt.UTC(), UpdatedAt: row.Case.CreatedAt.UTC()}, CaseID: caseID, GuildID: batch.GuildID, EventType: quack.CaseEventCreated, ActorType: "system", Visibility: quack.EventVisibilityStaff, Body: "Imported historical v4 case", MetadataJSON: `{"historical":true,"source":"v4_import"}`}
			if err := tx.Select("*").Create(&event).Error; err != nil {
				return fmt.Errorf("create imported case event: %w", err)
			}
			sourceID := quack.NewID()
			mapping := V4ImportSourceRecord{ID: sourceID, BatchID: batch.ID, GuildID: batch.GuildID, SourceName: batch.SourceName, SourceID: row.Case.SourceID, SourceCaseNumber: row.Case.CaseNumber, TargetCaseID: caseID, Fingerprint: row.Fingerprint, CreatedAt: now}
			if err := tx.Create(&mapping).Error; err != nil {
				return fmt.Errorf("record v4 source mapping: %w", err)
			}
			decision.TargetCaseID, decision.Created, decision.WouldCreate = caseID, true, false
			created++
		}
		ledger := V4ImportBatchRecord{ID: batch.ID, GuildID: batch.GuildID, SourceName: batch.SourceName, Checksum: batch.Checksum, ActorDiscordUserID: batch.ActorDiscordUserID, RecordCount: batch.RecordCount, CreatedCount: created, AlreadyImportedCount: already, WarningCount: warnings, CreatedAt: now}
		if err := tx.Create(&ledger).Error; err != nil {
			return fmt.Errorf("record v4 import batch: %w", err)
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: batch.GuildID, ActorDiscordUserID: batch.ActorDiscordUserID, Source: quack.AuditSourceSystem, Action: "v4_import.batch", ResourceType: "v4_import_batch", ResourceID: batch.ID, Result: quack.AuditResultSuccess, MetadataJSON: fmt.Sprintf(`{"checksum":"%s","records":%d,"created":%d,"already_imported":%d,"warnings":%d}`, batch.Checksum, batch.RecordCount, created, already, warnings)}, now)
	})
	return decisions, err
}

func inspectV4Rows(db *gorm.DB, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	used := map[uint64]bool{}
	var numbers []uint64
	if err := db.Model(&quack.Case{}).Where("guild_id = ?", batch.GuildID).Pluck("case_number", &numbers).Error; err != nil {
		return nil, err
	}
	var max uint64
	for _, n := range numbers {
		used[n] = true
		if n > max {
			max = n
		}
	}
	decisions := make([]v4import.Decision, len(rows))
	for index, row := range rows {
		decision := v4import.Decision{Line: row.Line, SourceID: row.Case.SourceID, SourceCaseNumber: row.Case.CaseNumber}
		var existing V4ImportSourceRecord
		result := db.Where("guild_id = ? AND source_name = ? AND source_id = ?", batch.GuildID, batch.SourceName, row.Case.SourceID).First(&existing)
		if result.Error == nil {
			if existing.Fingerprint != row.Fingerprint {
				return nil, fmt.Errorf("%w: line %d", v4import.ErrSourceCollision, row.Line)
			}
			decision.TargetCaseID, decision.TargetCaseNumber, decision.AlreadyImported = existing.TargetCaseID, caseNumberForID(db, existing.TargetCaseID), true
			decisions[index] = decision
			continue
		}
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return nil, result.Error
		}
		number := row.Case.CaseNumber
		if number == 0 || used[number] {
			max++
			number = max
			decision.Warnings = append(decision.Warnings, "case_number_remapped")
		} else if number > max {
			max = number
		}
		used[number] = true
		decision.TargetCaseNumber, decision.WouldCreate = number, true
		if row.Case.ModeratorDiscordUserID == "" {
			decision.Warnings = append(decision.Warnings, "moderator_identity_unavailable")
		}
		if row.Case.TargetDeparted {
			decision.Warnings = append(decision.Warnings, "target_departed")
		}
		if row.Case.TargetMissing {
			decision.Warnings = append(decision.Warnings, "target_missing")
		}
		if row.Case.ActionExpiresAt != nil && row.Case.ActionExpiresAt.Before(time.Now().UTC()) {
			decision.Warnings = append(decision.Warnings, "expired_action_manual_review")
		}
		decisions[index] = decision
	}
	return decisions, nil
}

func caseNumberForID(db *gorm.DB, caseID string) uint64 {
	var item quack.Case
	_ = db.Select("case_number").First(&item, "id = ?", caseID).Error
	return item.CaseNumber
}

// RollbackV4Import removes only untouched historical projections from one batch and leaves an audit trail.
func (s *Store) RollbackV4Import(ctx context.Context, guildID, batchID, actorID string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var mappings []V4ImportSourceRecord
		if err := tx.Where("guild_id = ? AND batch_id = ?", guildID, batchID).Find(&mappings).Error; err != nil {
			return err
		}
		if len(mappings) == 0 {
			return nil
		}
		ids := make([]string, len(mappings))
		for index := range mappings {
			ids[index] = mappings[index].TargetCaseID
		}
		sort.Strings(ids)
		for _, table := range []string{"case_action_executions", "case_notifications", "appeals", "case_evidence_snapshots"} {
			var count int64
			column := "case_id"
			if err := tx.Table(table).Where(column+" IN ?", ids).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("cannot roll back import batch with dependent %s rows", table)
			}
		}
		if err := tx.Where("case_id IN ?", ids).Delete(&quack.CaseEvent{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ? AND source = ?", ids, quack.CaseSourceV4Import).Delete(&quack.Case{}).Error; err != nil {
			return err
		}
		if err := tx.Where("guild_id = ? AND batch_id = ?", guildID, batchID).Delete(&V4ImportSourceRecord{}).Error; err != nil {
			return err
		}
		if err := tx.Where("guild_id = ? AND id = ?", guildID, batchID).Delete(&V4ImportBatchRecord{}).Error; err != nil {
			return err
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{GuildID: guildID, ActorDiscordUserID: actorID, Source: quack.AuditSourceSystem, Action: "v4_import.rollback", ResourceType: "v4_import_batch", ResourceID: batchID, Result: quack.AuditResultSuccess, MetadataJSON: fmt.Sprintf(`{"removed_cases":%d}`, len(ids))}, time.Now().UTC())
	})
}

// RecordV4ImportFailure audits only bounded failure classification and counts.
func (s *Store) RecordV4ImportFailure(ctx context.Context, batch v4import.Batch, failures int, code string) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}
	return createAuditLogEntry(s.db.WithContext(ctx), &quack.AuditLogEntry{GuildID: batch.GuildID, ActorDiscordUserID: batch.ActorDiscordUserID, Source: quack.AuditSourceSystem, Action: "v4_import.batch", ResourceType: "v4_import_batch", ResourceID: batch.ID, Result: quack.AuditResultFailure, FailureReason: code, MetadataJSON: fmt.Sprintf(`{"checksum":"%s","records":%d,"failures":%d}`, batch.Checksum, batch.RecordCount, failures)}, time.Now().UTC())
}
