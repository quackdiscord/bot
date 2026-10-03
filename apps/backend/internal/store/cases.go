package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CreateCase saves a case with its first event, executions, evidence,
// notification, and audit entries in one transaction, numbering it next in
// its guild. Callers hold the guild case lock (see WithGuildCaseLock); the
// unique (guild_id, case_number) index backs that up.
func (s *Store) CreateCase(ctx context.Context, params quack.CreateCaseParams) (*quack.CreatedCase, error) {
	now := time.Now().UTC()
	c := params.Case
	stamp(&c.ULIDModel, now)
	c.Validity = cmp.Or(c.Validity, quack.CaseValidityValid)
	c.Source = cmp.Or(c.Source, quack.CaseSourceDashboard)
	c.TemplateSnapshotJSON = cmp.Or(c.TemplateSnapshotJSON, "{}")
	c.MetadataJSON = cmp.Or(c.MetadataJSON, "{}")
	c.ContextValuesJSON = cmp.Or(c.ContextValuesJSON, "[]")

	event := params.Event
	executions := append([]quack.CaseActionExecution(nil), params.ActionExecutions...)
	evidence := append([]quack.CaseEvidenceSnapshot(nil), params.Evidence...)
	attachments := append([]quack.CaseEvidenceAttachment(nil), params.Attachments...)
	var notification *quack.CaseNotification

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var latest uint64
		if err := tx.Model(&caseRecord{}).Where("guild_id = ?", c.GuildID).
			Select("COALESCE(MAX(case_number), 0)").Scan(&latest).Error; err != nil {
			return fmt.Errorf("get latest case number: %w", err)
		}
		c.CaseNumber = latest + 1
		record := newCaseRecord(c)
		if err := tx.Create(&record).Error; err != nil {
			return fmt.Errorf("create case: %w", err)
		}

		event.CaseID, event.GuildID = c.ID, c.GuildID
		event.EventType = cmp.Or(event.EventType, quack.CaseEventCreated)
		event.ActorType = cmp.Or(event.ActorType, "staff")
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}

		executionRecords := make([]executionRecord, len(executions))
		for i := range executions {
			e := &executions[i]
			e.CaseID = c.ID
			e.Status = cmp.Or(e.Status, quack.ActionExecutionPending)
			e.ConfigSnapshotJSON = cmp.Or(e.ConfigSnapshotJSON, "{}")
			e.IdempotencyKey = cmp.Or(e.IdempotencyKey, fmt.Sprintf("case:%s:action:%d", c.ID, e.Position))
			e.CorrelationID = cmp.Or(e.CorrelationID, c.CorrelationID)
			stamp(&e.ULIDModel, now)
			executionRecords[i] = newExecutionRecord(*e)
		}
		if err := createAll(tx, executionRecords); err != nil {
			return fmt.Errorf("create case action executions: %w", err)
		}

		evidenceRecords := make([]evidenceRecord, len(evidence))
		for i := range evidence {
			e := &evidence[i]
			e.CaseID, e.GuildID = c.ID, c.GuildID
			e.EmbedsJSON = cmp.Or(e.EmbedsJSON, "[]")
			stamp(&e.ULIDModel, now)
			evidenceRecords[i] = newEvidenceRecord(*e)
		}
		if err := createAll(tx, evidenceRecords); err != nil {
			return fmt.Errorf("create case evidence: %w", err)
		}
		attachmentRecords := make([]attachmentRecord, len(attachments))
		for i := range attachments {
			if attachments[i].EvidenceID == "" {
				return errors.New("evidence attachment has no snapshot")
			}
			stamp(&attachments[i].ULIDModel, now)
			attachmentRecords[i] = newAttachmentRecord(attachments[i])
		}
		if err := createAll(tx, attachmentRecords); err != nil {
			return fmt.Errorf("create case evidence attachments: %w", err)
		}

		if params.Notification != nil {
			n := *params.Notification
			n.CaseID = c.ID
			n.Status = cmp.Or(n.Status, quack.NotificationPending)
			stamp(&n.ULIDModel, now)
			record := newCaseNotificationRecord(n)
			if err := tx.Create(&record).Error; err != nil {
				return fmt.Errorf("create case notification: %w", err)
			}
			notification = &n
		}

		if c.ReplacesCaseID != nil {
			result := tx.Model(&caseRecord{}).
				Where("id = ? AND guild_id = ? AND validity = ? AND replacement_case_id IS NULL",
					*c.ReplacesCaseID, c.GuildID, quack.CaseValidityVoided).
				Update("replacement_case_id", c.ID)
			if result.Error != nil {
				return fmt.Errorf("link replacement case: %w", result.Error)
			}
			if result.RowsAffected != 1 {
				return errors.New("replacement case is no longer available")
			}
		}

		if err := writeAudit(tx, params.Audit, c.ID, now); err != nil {
			return err
		}
		for _, entry := range params.AdditionalAudits {
			resourceID := entry.ResourceID
			if resourceID == "" || resourceID == "unknown" {
				resourceID = c.ID
			}
			if err := writeAudit(tx, &entry, resourceID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &quack.CreatedCase{
		Case:             c,
		Event:            event,
		ActionExecutions: executions,
		Evidence:         evidence,
		Attachments:      attachments,
		Notification:     notification,
	}, nil
}

// VoidCase voids a valid case so it stops counting toward escalation, and
// cancels its unstarted work. Voiding an already voided case with the same
// reason is a no-op; with a different reason it fails. It returns nil when
// the case is not in the guild.
func (s *Store) VoidCase(ctx context.Context, params quack.VoidCaseParams) (*quack.Case, error) {
	now := time.Now().UTC()
	var record caseRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).Where("id = ? AND guild_id = ?", params.CaseID, params.GuildID), &record)
		if err != nil {
			return fmt.Errorf("get case: %w", err)
		}
		if !found {
			return errNotFound
		}
		if record.Validity == quack.CaseValidityVoided {
			if record.VoidedReason == params.Reason {
				return nil
			}
			return errors.New("case is already voided with a different reason")
		}
		if err := voidCase(tx, &record, params.Reason, params.ActorDiscordUserID, params.ReplacementCaseID, now); err != nil {
			return err
		}
		event := quack.CaseEvent{
			CaseID:             record.ID,
			GuildID:            record.GuildID,
			EventType:          quack.CaseEventVoided,
			ActorDiscordUserID: params.ActorDiscordUserID,
			ActorType:          "staff",
			Visibility:         quack.EventVisibilityPublic,
			Body:               "Case voided",
			MetadataJSON: jsonObject(map[string]any{
				"reason":              params.Reason,
				"replacement_case_id": params.ReplacementCaseID,
			}),
		}
		if err := appendCaseEvent(tx, &event, now); err != nil {
			return err
		}
		return writeAudit(tx, params.Audit, record.ID, now)
	})
	if err != nil || record.ID == "" {
		return nil, notFoundIsNil(err)
	}
	item := record.model()
	return &item, nil
}

// errCaseNotValid means a case changed validity under a caller that expected
// it to still be valid.
var errCaseNotValid = errors.New("case is no longer valid")

// voidCase marks a locked, valid case voided and cancels the work it has not
// started: pending and retrying executions and an unsent notification.
// Running executions and a notification already being sent are left alone,
// because their Discord requests may already have happened. Both case voids
// and accepted appeals go through here.
func voidCase(tx *gorm.DB, c *caseRecord, reason, actorID string, replacementID *string, now time.Time) error {
	result := tx.Model(&caseRecord{}).
		Where("id = ? AND validity = ?", c.ID, quack.CaseValidityValid).
		Updates(map[string]any{
			"validity":                  quack.CaseValidityVoided,
			"voided_reason":             reason,
			"voided_by_discord_user_id": actorID,
			"voided_at":                 now,
			"replacement_case_id":       replacementID,
			"updated_at":                now,
		})
	if result.Error != nil {
		return fmt.Errorf("void case: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return errCaseNotValid
	}
	c.Validity = quack.CaseValidityVoided
	c.VoidedReason = reason
	c.VoidedByDiscordUserID = actorID
	c.VoidedAt = &now
	c.ReplacementCaseID = replacementID
	c.UpdatedAt = now

	unstarted := []quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRetrying}
	if err := tx.Model(&executionRecord{}).
		Where("case_id = ? AND status IN ?", c.ID, unstarted).
		Updates(map[string]any{
			"status":          quack.ActionExecutionCancelled,
			"last_error_code": "case_voided",
			"last_error":      "case was voided before enforcement",
			"finished_at":     now,
			"next_retry_at":   nil,
			"updated_at":      now,
		}).Error; err != nil {
		return fmt.Errorf("cancel voided case actions: %w", err)
	}
	unsent := []quack.NotificationStatus{quack.NotificationPending, quack.NotificationPrepared, quack.NotificationClaimed}
	if err := tx.Model(&caseNotificationRecord{}).
		Where("case_id = ? AND status IN ?", c.ID, unsent).
		Updates(map[string]any{
			"status":           quack.NotificationFailed,
			"last_error_code":  "case_voided",
			"last_error":       "case was voided before notification",
			"lease_token":      "",
			"lease_expires_at": nil,
			"updated_at":       now,
		}).Error; err != nil {
		return fmt.Errorf("cancel voided case notification: %w", err)
	}
	return nil
}

// appendCaseEvent adds event to its case's timeline, filling in defaults and,
// when the caller left it empty, the guild ID.
func appendCaseEvent(tx *gorm.DB, event *quack.CaseEvent, now time.Time) error {
	if event.GuildID == "" {
		var c caseRecord
		found, err := first(tx.Select("guild_id").Where("id = ?", event.CaseID), &c)
		if err != nil {
			return fmt.Errorf("get case for event: %w", err)
		}
		if !found {
			return fmt.Errorf("get case for event: case %s not found", event.CaseID)
		}
		event.GuildID = c.GuildID
	}
	event.Visibility = cmp.Or(event.Visibility, quack.EventVisibilityStaff)
	event.ActorType = cmp.Or(event.ActorType, "system")
	event.MetadataJSON = cmp.Or(event.MetadataJSON, "{}")
	stamp(&event.ULIDModel, now)
	record := newCaseEventRecord(*event)
	if err := tx.Create(&record).Error; err != nil {
		return fmt.Errorf("create case event: %w", err)
	}
	return nil
}

func newCaseRecord(c quack.Case) caseRecord {
	return caseRecord{
		ID:                      c.ID,
		CreatedAt:               c.CreatedAt,
		UpdatedAt:               c.UpdatedAt,
		GuildID:                 c.GuildID,
		CaseNumber:              c.CaseNumber,
		TemplateID:              c.TemplateID,
		TemplateVersion:         c.TemplateVersion,
		TemplateSnapshotJSON:    c.TemplateSnapshotJSON,
		TargetDiscordUserID:     c.TargetDiscordUserID,
		ModeratorDiscordUserID:  c.ModeratorDiscordUserID,
		Reason:                  c.Reason,
		Validity:                c.Validity,
		Source:                  c.Source,
		CorrelationID:           c.CorrelationID,
		ContextChannelDiscordID: c.ContextChannelDiscordID,
		ContextMessageDiscordID: c.ContextMessageDiscordID,
		ContextURL:              c.ContextURL,
		MetadataJSON:            c.MetadataJSON,
		ContextValuesJSON:       c.ContextValuesJSON,
		VoidedReason:            c.VoidedReason,
		VoidedByDiscordUserID:   c.VoidedByDiscordUserID,
		VoidedAt:                c.VoidedAt,
		ReplacementCaseID:       c.ReplacementCaseID,
		ReplacesCaseID:          c.ReplacesCaseID,
		IdempotencyKey:          c.IdempotencyKey,
	}
}

func (r caseRecord) model() quack.Case {
	return quack.Case{
		ULIDModel:               ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                 r.GuildID,
		CaseNumber:              r.CaseNumber,
		TemplateID:              r.TemplateID,
		TemplateVersion:         r.TemplateVersion,
		TemplateSnapshotJSON:    r.TemplateSnapshotJSON,
		TargetDiscordUserID:     r.TargetDiscordUserID,
		ModeratorDiscordUserID:  r.ModeratorDiscordUserID,
		Reason:                  r.Reason,
		Validity:                r.Validity,
		Source:                  r.Source,
		CorrelationID:           r.CorrelationID,
		ContextChannelDiscordID: r.ContextChannelDiscordID,
		ContextMessageDiscordID: r.ContextMessageDiscordID,
		ContextURL:              r.ContextURL,
		MetadataJSON:            r.MetadataJSON,
		ContextValuesJSON:       r.ContextValuesJSON,
		VoidedReason:            r.VoidedReason,
		VoidedByDiscordUserID:   r.VoidedByDiscordUserID,
		VoidedAt:                r.VoidedAt,
		ReplacementCaseID:       r.ReplacementCaseID,
		ReplacesCaseID:          r.ReplacesCaseID,
		IdempotencyKey:          r.IdempotencyKey,
	}
}

func newCaseEventRecord(e quack.CaseEvent) caseEventRecord {
	return caseEventRecord{
		ID:                 e.ID,
		CreatedAt:          e.CreatedAt,
		UpdatedAt:          e.UpdatedAt,
		CaseID:             e.CaseID,
		GuildID:            e.GuildID,
		EventType:          e.EventType,
		ActorDiscordUserID: e.ActorDiscordUserID,
		ActorType:          e.ActorType,
		Visibility:         e.Visibility,
		Body:               e.Body,
		MetadataJSON:       e.MetadataJSON,
	}
}

func (r caseEventRecord) model() quack.CaseEvent {
	return quack.CaseEvent{
		ULIDModel:          ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		CaseID:             r.CaseID,
		GuildID:            r.GuildID,
		EventType:          r.EventType,
		ActorDiscordUserID: r.ActorDiscordUserID,
		ActorType:          r.ActorType,
		Visibility:         r.Visibility,
		Body:               r.Body,
		MetadataJSON:       r.MetadataJSON,
	}
}

func newEvidenceRecord(e quack.CaseEvidenceSnapshot) evidenceRecord {
	return evidenceRecord{
		ID:                  e.ID,
		CreatedAt:           e.CreatedAt,
		UpdatedAt:           e.UpdatedAt,
		CaseID:              e.CaseID,
		GuildID:             e.GuildID,
		ChannelDiscordID:    e.ChannelDiscordID,
		MessageDiscordID:    e.MessageDiscordID,
		AuthorDiscordUserID: e.AuthorDiscordUserID,
		MessageURL:          e.MessageURL,
		Content:             e.Content,
		MessageCreatedAt:    e.MessageCreatedAt,
		MessageEditedAt:     e.MessageEditedAt,
		EmbedsJSON:          e.EmbedsJSON,
		CaptureOutcome:      e.CaptureOutcome,
		CaptureWarning:      e.CaptureWarning,
	}
}

func (r evidenceRecord) model() quack.CaseEvidenceSnapshot {
	return quack.CaseEvidenceSnapshot{
		ULIDModel:           ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		CaseID:              r.CaseID,
		GuildID:             r.GuildID,
		ChannelDiscordID:    r.ChannelDiscordID,
		MessageDiscordID:    r.MessageDiscordID,
		AuthorDiscordUserID: r.AuthorDiscordUserID,
		MessageURL:          r.MessageURL,
		Content:             r.Content,
		MessageCreatedAt:    r.MessageCreatedAt,
		MessageEditedAt:     r.MessageEditedAt,
		EmbedsJSON:          r.EmbedsJSON,
		CaptureOutcome:      r.CaptureOutcome,
		CaptureWarning:      r.CaptureWarning,
	}
}

func newAttachmentRecord(a quack.CaseEvidenceAttachment) attachmentRecord {
	return attachmentRecord{
		ID:                           a.ID,
		CreatedAt:                    a.CreatedAt,
		UpdatedAt:                    a.UpdatedAt,
		EvidenceID:                   a.EvidenceID,
		Filename:                     a.Filename,
		ContentType:                  a.ContentType,
		SizeBytes:                    a.SizeBytes,
		OriginalURL:                  a.OriginalURL,
		PreservedURL:                 a.PreservedURL,
		PreservedMessageDiscordID:    a.PreservedMessageDiscordID,
		PreservedAttachmentDiscordID: a.PreservedAttachmentDiscordID,
		CopyOutcome:                  a.CopyOutcome,
		Warning:                      a.Warning,
	}
}

func (r attachmentRecord) model() quack.CaseEvidenceAttachment {
	return quack.CaseEvidenceAttachment{
		ULIDModel:                    ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		EvidenceID:                   r.EvidenceID,
		Filename:                     r.Filename,
		ContentType:                  r.ContentType,
		SizeBytes:                    r.SizeBytes,
		OriginalURL:                  r.OriginalURL,
		PreservedURL:                 r.PreservedURL,
		PreservedMessageDiscordID:    r.PreservedMessageDiscordID,
		PreservedAttachmentDiscordID: r.PreservedAttachmentDiscordID,
		CopyOutcome:                  r.CopyOutcome,
		Warning:                      r.Warning,
	}
}
