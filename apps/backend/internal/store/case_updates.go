package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// AppendCaseEvidence adds evidence to an existing case with its audit entry
// and requests a refresh of the case's publications. Only evidence tables
// change: the decision and its enforcement are untouched.
func (s *Store) AppendCaseEvidence(ctx context.Context, params quack.AppendCaseEvidenceParams) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c caseRecord
		found, err := first(forUpdate(tx).Where("id = ? AND guild_id = ?", params.CaseID, params.GuildID), &c)
		if err != nil {
			return fmt.Errorf("get case for evidence: %w", err)
		}
		if !found {
			return fmt.Errorf("get case for evidence: %w", quack.ErrCaseNotFound)
		}
		ids := make(map[string]bool, len(params.Evidence))
		evidence := make([]evidenceRecord, len(params.Evidence))
		for i, e := range params.Evidence {
			e.CaseID, e.GuildID = c.ID, c.GuildID
			e.EmbedsJSON = cmp.Or(e.EmbedsJSON, "[]")
			stamp(&e.ULIDModel, now)
			ids[e.ID] = true
			evidence[i] = newEvidenceRecord(e)
		}
		if err := createAll(tx, evidence); err != nil {
			return fmt.Errorf("create case evidence: %w", err)
		}
		attachments := make([]attachmentRecord, len(params.Attachments))
		for i, a := range params.Attachments {
			if !ids[a.EvidenceID] {
				return errors.New("evidence attachment does not belong to this evidence")
			}
			stamp(&a.ULIDModel, now)
			attachments[i] = newAttachmentRecord(a)
		}
		if err := createAll(tx, attachments); err != nil {
			return fmt.Errorf("create case evidence attachments: %w", err)
		}
		if err := requestPublicationRefresh(tx, c.ID, now); err != nil {
			return err
		}
		if err := appendCaseUpdateEvent(tx, c, quack.CaseEventEvidenceAdded, "Evidence added", params.Audit, now); err != nil {
			return err
		}
		return writeCaseUpdateAudit(tx, params.Audit, c, map[string]any{"evidence_added": len(evidence)}, now)
	})
}

// UpdateCaseContext replaces a case's context values with its audit entry
// and requests a refresh of the case's publications. The row lock keeps it
// from overwriting a concurrent void. It returns nil when the case is not in
// the guild.
func (s *Store) UpdateCaseContext(ctx context.Context, params quack.UpdateCaseContextParams) (*quack.Case, error) {
	now := time.Now().UTC()
	var record caseRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := forUpdate(tx).Where("guild_id = ?", params.GuildID)
		if number, err := strconv.ParseUint(params.CaseRef, 10, 64); err == nil {
			query = query.Where("case_number = ?", number)
		} else {
			query = query.Where("id = ?", params.CaseRef)
		}
		found, err := first(query, &record)
		if err != nil {
			return fmt.Errorf("get case for context update: %w", err)
		}
		if !found {
			return errNotFound
		}
		record.ContextValuesJSON = cmp.Or(params.ContextValuesJSON, "[]")
		record.UpdatedAt = now
		if err := tx.Model(&caseRecord{}).Where("id = ?", record.ID).Updates(map[string]any{
			"context_values_json": record.ContextValuesJSON,
			"updated_at":          now,
		}).Error; err != nil {
			return fmt.Errorf("update case context: %w", err)
		}
		if err := requestPublicationRefresh(tx, record.ID, now); err != nil {
			return err
		}
		if err := appendCaseUpdateEvent(tx, record, quack.CaseEventContextUpdated, "Context updated", params.Audit, now); err != nil {
			return err
		}
		return writeCaseUpdateAudit(tx, params.Audit, record, nil, now)
	})
	if err != nil || record.ID == "" {
		return nil, notFoundIsNil(err)
	}
	item := record.model()
	return &item, nil
}

// appendCaseUpdateEvent adds a staff timeline event for a case update,
// attributed to the audit entry's actor.
func appendCaseUpdateEvent(tx *gorm.DB, c caseRecord, eventType quack.CaseEventType, body string, audit *quack.AuditLogEntry, now time.Time) error {
	event := quack.CaseEvent{
		CaseID:     c.ID,
		GuildID:    c.GuildID,
		EventType:  eventType,
		ActorType:  "staff",
		Visibility: quack.EventVisibilityStaff,
		Body:       body,
	}
	if audit != nil {
		event.ActorDiscordUserID = audit.ActorDiscordUserID
	}
	return appendCaseEvent(tx, &event, now)
}

// writeCaseUpdateAudit writes a case update's audit entry with the case's
// identity and extra in its metadata.
func writeCaseUpdateAudit(tx *gorm.DB, audit *quack.AuditLogEntry, c caseRecord, extra map[string]any, now time.Time) error {
	if audit == nil {
		return nil
	}
	entry := *audit
	entry.GuildID = c.GuildID
	metadata := map[string]any{
		"case_id":                c.ID,
		"case_number":            c.CaseNumber,
		"target_discord_user_id": c.TargetDiscordUserID,
	}
	for key, value := range extra {
		metadata[key] = value
	}
	entry.MetadataJSON = jsonObject(metadata)
	return writeAudit(tx, &entry, c.ID, now)
}
