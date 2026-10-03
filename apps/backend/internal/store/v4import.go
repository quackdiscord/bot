package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/v4import"
	"gorm.io/gorm"
)

// PreviewV4Import reports what importing rows would do, without writing.
func (s *Store) PreviewV4Import(ctx context.Context, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	return inspectV4Rows(s.db.WithContext(ctx), batch, rows)
}

// ApplyV4Import imports rows as historical cases in one transaction, along
// with their source mappings, the batch ledger row, and an audit entry.
// Imported cases are valid but have source v4_import, so they never count
// toward escalation. Re-applying a committed batch changes nothing.
func (s *Store) ApplyV4Import(ctx context.Context, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	var decisions []v4import.Decision
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var guild guildRecord
		found, err := first(forUpdate(tx).Where("id = ?", batch.GuildID), &guild)
		if err != nil {
			return fmt.Errorf("lock import guild: %w", err)
		}
		if !found {
			return fmt.Errorf("lock import guild: guild %s not found", batch.GuildID)
		}
		var existing v4BatchRecord
		committed, err := first(tx.Where("guild_id = ? AND source_name = ? AND checksum = ?",
			batch.GuildID, batch.SourceName, batch.Checksum), &existing)
		if err != nil {
			return fmt.Errorf("get import batch: %w", err)
		}
		decisions, err = inspectV4Rows(tx, batch, rows)
		if err != nil {
			return err
		}
		if committed {
			for _, decision := range decisions {
				if !decision.AlreadyImported {
					return errors.New("v4 import batch ledger is incomplete")
				}
			}
			return nil
		}

		now := time.Now().UTC()
		created, already, warnings := 0, 0, 0
		for i, row := range rows {
			decision := &decisions[i]
			warnings += len(decision.Warnings)
			if decision.AlreadyImported {
				already++
				continue
			}
			caseID, err := createImportedCase(tx, batch, row, decision.TargetCaseNumber)
			if err != nil {
				return err
			}
			mapping := v4SourceRecord{
				ID:               quack.NewID(),
				CreatedAt:        now,
				BatchID:          batch.ID,
				GuildID:          batch.GuildID,
				SourceName:       batch.SourceName,
				SourceID:         row.Case.SourceID,
				SourceCaseNumber: row.Case.CaseNumber,
				TargetCaseID:     caseID,
				Fingerprint:      row.Fingerprint,
			}
			if err := tx.Create(&mapping).Error; err != nil {
				return fmt.Errorf("record v4 source mapping: %w", err)
			}
			decision.TargetCaseID, decision.Created, decision.WouldCreate = caseID, true, false
			created++
		}
		ledger := v4BatchRecord{
			ID:                   batch.ID,
			CreatedAt:            now,
			GuildID:              batch.GuildID,
			SourceName:           batch.SourceName,
			Checksum:             batch.Checksum,
			ActorDiscordUserID:   batch.ActorDiscordUserID,
			RecordCount:          batch.RecordCount,
			CreatedCount:         created,
			AlreadyImportedCount: already,
			WarningCount:         warnings,
		}
		if err := tx.Create(&ledger).Error; err != nil {
			return fmt.Errorf("record v4 import batch: %w", err)
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{
			GuildID:            batch.GuildID,
			ActorDiscordUserID: batch.ActorDiscordUserID,
			Source:             quack.AuditSourceSystem,
			Action:             "v4_import.batch",
			ResourceType:       "v4_import_batch",
			ResourceID:         batch.ID,
			Result:             quack.AuditResultSuccess,
			MetadataJSON: jsonObject(map[string]any{
				"checksum":         batch.Checksum,
				"records":          batch.RecordCount,
				"created":          created,
				"already_imported": already,
				"warnings":         warnings,
			}),
		}, now)
	})
	return decisions, err
}

// createImportedCase writes one v4 case and its created event, both dated at
// the original v4 time.
func createImportedCase(tx *gorm.DB, batch v4import.Batch, row v4import.PreparedCase, number uint64) (string, error) {
	v4 := row.Case
	at := v4.CreatedAt.UTC()
	c := quack.Case{
		ULIDModel:              quack.ULIDModel{ID: quack.NewID(), CreatedAt: at, UpdatedAt: at},
		GuildID:                batch.GuildID,
		CaseNumber:             number,
		TemplateSnapshotJSON:   jsonObject(map[string]any{"historical": true, "v4_action_type": v4.ActionType}),
		TargetDiscordUserID:    v4.TargetDiscordUserID,
		ModeratorDiscordUserID: v4.ModeratorDiscordUserID,
		Reason:                 v4.Reason,
		Validity:               quack.CaseValidityValid,
		Source:                 quack.CaseSourceV4Import,
		ContextURL:             v4.ContextURL,
		ContextValuesJSON:      "[]",
		MetadataJSON: jsonObject(map[string]any{"historical": true, "v4": map[string]any{
			"source_name":            batch.SourceName,
			"source_id":              v4.SourceID,
			"case_number":            v4.CaseNumber,
			"action_type":            v4.ActionType,
			"moderator_display_name": v4.ModeratorDisplayName,
			"target_departed":        v4.TargetDeparted,
			"target_missing":         v4.TargetMissing,
			"action_expires_at":      v4.ActionExpiresAt,
		}}),
	}
	record := newCaseRecord(c)
	if err := tx.Create(&record).Error; err != nil {
		return "", fmt.Errorf("create imported case: %w", err)
	}
	event := quack.CaseEvent{
		ULIDModel:    quack.ULIDModel{CreatedAt: at},
		CaseID:       c.ID,
		GuildID:      c.GuildID,
		EventType:    quack.CaseEventCreated,
		Visibility:   quack.EventVisibilityStaff,
		Body:         "Imported historical v4 case",
		MetadataJSON: `{"historical":true,"source":"v4_import"}`,
	}
	if err := appendCaseEvent(tx, &event, at); err != nil {
		return "", err
	}
	return c.ID, nil
}

// inspectV4Rows decides, for each row, whether it was already imported and
// which case number it gets. A v4 number is kept when it is free in the guild
// and remapped past the highest number otherwise. It reads everything it
// needs up front rather than querying per row.
func inspectV4Rows(db *gorm.DB, batch v4import.Batch, rows []v4import.PreparedCase) ([]v4import.Decision, error) {
	var numbers []uint64
	if err := db.Model(&caseRecord{}).Where("guild_id = ?", batch.GuildID).
		Pluck("case_number", &numbers).Error; err != nil {
		return nil, fmt.Errorf("list guild case numbers: %w", err)
	}
	used := make(map[uint64]bool, len(numbers))
	var highest uint64
	for _, n := range numbers {
		used[n] = true
		highest = max(highest, n)
	}

	sourceIDs := make([]string, len(rows))
	for i, row := range rows {
		sourceIDs[i] = row.Case.SourceID
	}
	mappings := make(map[string]v4SourceRecord, len(rows))
	for chunk := range slices.Chunk(sourceIDs, 500) {
		var found []v4SourceRecord
		if err := db.Where("guild_id = ? AND source_name = ? AND source_id IN ?", batch.GuildID, batch.SourceName, chunk).
			Find(&found).Error; err != nil {
			return nil, fmt.Errorf("list v4 source mappings: %w", err)
		}
		for _, m := range found {
			mappings[m.SourceID] = m
		}
	}
	targetIDs := make([]string, 0, len(mappings))
	for _, m := range mappings {
		targetIDs = append(targetIDs, m.TargetCaseID)
	}
	targetNumbers := make(map[string]uint64, len(targetIDs))
	for chunk := range slices.Chunk(targetIDs, 500) {
		var targets []caseRecord
		if err := db.Select("id", "case_number").Where("id IN ?", chunk).Find(&targets).Error; err != nil {
			return nil, fmt.Errorf("list imported case numbers: %w", err)
		}
		for _, t := range targets {
			targetNumbers[t.ID] = t.CaseNumber
		}
	}

	now := time.Now().UTC()
	decisions := make([]v4import.Decision, len(rows))
	for i, row := range rows {
		v4 := row.Case
		decision := v4import.Decision{Line: row.Line, SourceID: v4.SourceID, SourceCaseNumber: v4.CaseNumber}
		if existing, ok := mappings[v4.SourceID]; ok {
			if existing.Fingerprint != row.Fingerprint {
				return nil, fmt.Errorf("%w: line %d", v4import.ErrSourceCollision, row.Line)
			}
			decision.TargetCaseID = existing.TargetCaseID
			decision.TargetCaseNumber = targetNumbers[existing.TargetCaseID]
			decision.AlreadyImported = true
			decisions[i] = decision
			continue
		}
		number := v4.CaseNumber
		if number == 0 || used[number] {
			highest++
			number = highest
			decision.Warnings = append(decision.Warnings, "case_number_remapped")
		}
		highest = max(highest, number)
		used[number] = true
		decision.TargetCaseNumber, decision.WouldCreate = number, true
		if v4.ModeratorDiscordUserID == "" {
			decision.Warnings = append(decision.Warnings, "moderator_identity_unavailable")
		}
		if v4.TargetDeparted {
			decision.Warnings = append(decision.Warnings, "target_departed")
		}
		if v4.TargetMissing {
			decision.Warnings = append(decision.Warnings, "target_missing")
		}
		if v4.ActionExpiresAt != nil && v4.ActionExpiresAt.Before(now) {
			decision.Warnings = append(decision.Warnings, "expired_action_manual_review")
		}
		decisions[i] = decision
	}
	return decisions, nil
}

// RollbackV4Import deletes the cases one batch imported, with their events
// and the batch's ledger rows, and audits the rollback. It refuses when any
// of those cases has since gained executions, notifications, appeals, or
// evidence.
func (s *Store) RollbackV4Import(ctx context.Context, guildID, batchID, actorID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []string
		if err := tx.Model(&v4SourceRecord{}).Where("guild_id = ? AND batch_id = ?", guildID, batchID).
			Order("target_case_id").Pluck("target_case_id", &ids).Error; err != nil {
			return fmt.Errorf("list imported cases: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		for _, table := range []string{"case_action_executions", "case_notifications", "appeals", "case_evidence_snapshots"} {
			var count int64
			if err := tx.Table(table).Where("case_id IN ?", ids).Count(&count).Error; err != nil {
				return fmt.Errorf("count dependent %s: %w", table, err)
			}
			if count != 0 {
				return fmt.Errorf("cannot roll back import batch with dependent %s rows", table)
			}
		}
		deletes := []struct {
			model any
			where string
			args  []any
		}{
			{&caseEventRecord{}, "case_id IN ?", []any{ids}},
			{&caseRecord{}, "id IN ? AND source = ?", []any{ids, quack.CaseSourceV4Import}},
			{&v4SourceRecord{}, "guild_id = ? AND batch_id = ?", []any{guildID, batchID}},
			{&v4BatchRecord{}, "guild_id = ? AND id = ?", []any{guildID, batchID}},
		}
		for _, d := range deletes {
			if err := tx.Where(d.where, d.args...).Delete(d.model).Error; err != nil {
				return fmt.Errorf("roll back import batch: %w", err)
			}
		}
		return createAuditLogEntry(tx, &quack.AuditLogEntry{
			GuildID:            guildID,
			ActorDiscordUserID: actorID,
			Source:             quack.AuditSourceSystem,
			Action:             "v4_import.rollback",
			ResourceType:       "v4_import_batch",
			ResourceID:         batchID,
			Result:             quack.AuditResultSuccess,
			MetadataJSON:       jsonObject(map[string]any{"removed_cases": len(ids)}),
		}, time.Now().UTC())
	})
}

// RecordV4ImportFailure audits a failed import by error code and counts only;
// row content never reaches the audit log.
func (s *Store) RecordV4ImportFailure(ctx context.Context, batch v4import.Batch, failures int, code string) error {
	return createAuditLogEntry(s.db.WithContext(ctx), &quack.AuditLogEntry{
		GuildID:            batch.GuildID,
		ActorDiscordUserID: batch.ActorDiscordUserID,
		Source:             quack.AuditSourceSystem,
		Action:             "v4_import.batch",
		ResourceType:       "v4_import_batch",
		ResourceID:         batch.ID,
		Result:             quack.AuditResultFailure,
		FailureReason:      code,
		MetadataJSON: jsonObject(map[string]any{
			"checksum": batch.Checksum,
			"records":  batch.RecordCount,
			"failures": failures,
		}),
	}, time.Now().UTC())
}
