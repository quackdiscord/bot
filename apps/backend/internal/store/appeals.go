package store

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CreateAppeal saves a member's appeal with its first appeal event, a public
// case event, an audit entry, and the staff notification. A case takes one
// appeal; the unique case_id makes a concurrent second one fail with
// ErrAppealAlreadyExists.
func (s *Store) CreateAppeal(ctx context.Context, params quack.CreateAppealParams) (*quack.Appeal, error) {
	appeal := params.Appeal
	if appeal.CaseID == nil || strings.TrimSpace(*appeal.CaseID) == "" {
		return nil, quack.ErrAppealCaseIneligible
	}
	now := time.Now().UTC()
	stamp(&appeal.ULIDModel, now)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var c caseRecord
		found, err := first(forUpdate(tx).Where("id = ? AND guild_id = ?", *appeal.CaseID, appeal.GuildID), &c)
		if err != nil {
			return fmt.Errorf("get appealed case: %w", err)
		}
		// The service already checked the case is appealable. Recheck only
		// what can change concurrently: the case may have been voided since.
		if !found || c.TargetDiscordUserID != appeal.TargetDiscordUserID || c.Validity != quack.CaseValidityValid {
			return quack.ErrAppealCaseIneligible
		}
		var existing int64
		if err := tx.Model(&appealRecord{}).Where("case_id = ?", c.ID).Count(&existing).Error; err != nil {
			return fmt.Errorf("count case appeals: %w", err)
		}
		if existing != 0 {
			return quack.ErrAppealAlreadyExists
		}
		record := newAppealRecord(appeal)
		if err := tx.Create(&record).Error; err != nil {
			if isDuplicate(err) {
				return quack.ErrAppealAlreadyExists
			}
			return fmt.Errorf("create appeal: %w", err)
		}
		caseEvent := params.CaseEvent
		caseEvent.CaseID, caseEvent.GuildID = c.ID, c.GuildID
		if err := appendCaseEvent(tx, &caseEvent, now); err != nil {
			return err
		}
		return appendAppealEvent(tx, appeal, appealStep{params.Event, params.Notification, params.Audit}, now)
	})
	if err != nil {
		return nil, err
	}
	return &appeal, nil
}

// GetAppealByID returns an appeal, or nil. Callers authorize access.
func (s *Store) GetAppealByID(ctx context.Context, appealID string) (*quack.Appeal, error) {
	return getAppeal(s.db.WithContext(ctx).Where("id = ?", appealID))
}

// GetAppealByCaseID returns a case's appeal, or nil.
func (s *Store) GetAppealByCaseID(ctx context.Context, caseID string) (*quack.Appeal, error) {
	return getAppeal(s.db.WithContext(ctx).Where("case_id = ?", caseID))
}

func getAppeal(query *gorm.DB) (*quack.Appeal, error) {
	var record appealRecord
	found, err := first(query, &record)
	if err != nil || !found {
		return nil, wrap("get appeal", err)
	}
	appeal := record.model()
	return &appeal, nil
}

// ListAppeals returns a page of a guild's appeals, newest first, optionally
// filtered by status.
func (s *Store) ListAppeals(ctx context.Context, params quack.AppealListParams) (*quack.AppealListResult, error) {
	limit, offset := page(params.Limit, params.Offset)
	query := s.db.WithContext(ctx).Model(&appealRecord{}).Where("guild_id = ?", params.GuildID)
	if params.Status != "" {
		query = query.Where("status = ?", params.Status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count appeals: %w", err)
	}
	var records []appealRecord
	if err := query.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list appeals: %w", err)
	}
	appeals := make([]quack.Appeal, len(records))
	for i, r := range records {
		appeals[i] = r.model()
	}
	return &quack.AppealListResult{Appeals: appeals, Total: total}, nil
}

// ListAppealEvents returns an appeal's timeline, oldest first.
func (s *Store) ListAppealEvents(ctx context.Context, appealID string) ([]quack.AppealEvent, error) {
	var records []appealEventRecord
	if err := s.db.WithContext(ctx).Where("appeal_id = ?", appealID).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list appeal events: %w", err)
	}
	events := make([]quack.AppealEvent, len(records))
	for i, r := range records {
		events[i] = r.model()
	}
	return events, nil
}

// AppendAppealInformation records the member's answer to a request for more
// information and returns the appeal to pending review.
func (s *Store) AppendAppealInformation(ctx context.Context, params quack.AppendAppealInformationParams) (*quack.Appeal, error) {
	step := appealStep{event: params.Event, notification: params.Notification, audit: params.Audit}
	step.event.Body = params.Body
	match := []any{"id = ? AND target_discord_user_id = ?", params.AppealID, params.TargetDiscordUserID}
	return s.transitionAppeal(ctx, match, step, func(_ *gorm.DB, a *appealRecord, _ time.Time) error {
		if a.Status != quack.AppealStatusNeedsInformation {
			return quack.ErrAppealStateConflict
		}
		a.Status = quack.AppealStatusPending
		return nil
	})
}

// TransitionAppeal applies a staff decision: it moves the appeal from one of
// params.AllowedFrom to params.To and, for an accepted appeal, voids the case
// in the same transaction.
func (s *Store) TransitionAppeal(ctx context.Context, params quack.TransitionAppealParams) (*quack.Appeal, error) {
	step := appealStep{event: params.Event, notification: params.Notification, audit: params.AppealAudit}
	match := []any{"id = ? AND guild_id = ?", params.AppealID, params.GuildID}
	return s.transitionAppeal(ctx, match, step, func(tx *gorm.DB, a *appealRecord, now time.Time) error {
		if !slices.Contains(params.AllowedFrom, a.Status) {
			return quack.ErrAppealStateConflict
		}
		if params.VoidCase {
			if err := voidAppealedCase(tx, a, params, now); err != nil {
				return err
			}
		}
		a.Status = params.To
		a.DecisionReason = params.Reason
		a.ReviewedByDiscordUserID = params.ActorDiscordUserID
		a.ReviewedAt = &now
		return nil
	})
}

// voidAppealedCase voids the case behind an accepted appeal.
func voidAppealedCase(tx *gorm.DB, a *appealRecord, params quack.TransitionAppealParams, now time.Time) error {
	if a.CaseID == nil {
		return quack.ErrAppealCaseIneligible
	}
	var c caseRecord
	found, err := first(forUpdate(tx).Where("id = ? AND guild_id = ?", *a.CaseID, params.GuildID), &c)
	if err != nil {
		return fmt.Errorf("get appealed case: %w", err)
	}
	if !found || c.Validity != quack.CaseValidityValid {
		return quack.ErrAppealStateConflict
	}
	if err := voidCase(tx, &c, "Appeal accepted", params.ActorDiscordUserID, nil, now); err != nil {
		if errors.Is(err, errCaseNotValid) {
			return quack.ErrAppealStateConflict
		}
		return err
	}
	if err := appendCaseEvent(tx, &quack.CaseEvent{
		CaseID:             c.ID,
		GuildID:            c.GuildID,
		EventType:          quack.CaseEventVoided,
		ActorDiscordUserID: params.ActorDiscordUserID,
		ActorType:          "staff",
		Visibility:         quack.EventVisibilityPublic,
		Body:               "Case voided after appeal accepted",
	}, now); err != nil {
		return err
	}
	return writeAudit(tx, params.CaseAudit, c.ID, now)
}

// appealStep is what every appeal change writes besides the appeal itself.
type appealStep struct {
	event        quack.AppealEvent
	notification quack.AppealNotification
	audit        quack.AuditLogEntry
}

// transitionAppeal locks the appeal matching match (a condition and its
// arguments), lets change update it, and saves it under its optimistic
// version together with step. A missing appeal is a state conflict.
func (s *Store) transitionAppeal(ctx context.Context, match []any, step appealStep,
	change func(*gorm.DB, *appealRecord, time.Time) error) (*quack.Appeal, error) {
	now := time.Now().UTC()
	var record appealRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		found, err := first(forUpdate(tx).Where(match[0], match[1:]...), &record)
		if err != nil {
			return fmt.Errorf("get appeal: %w", err)
		}
		if !found {
			return quack.ErrAppealStateConflict
		}
		if err := change(tx, &record, now); err != nil {
			return err
		}
		record.Version++
		record.UpdatedAt = now
		result := tx.Model(&appealRecord{}).Where("id = ? AND version = ?", record.ID, record.Version-1).Updates(map[string]any{
			"status":                      record.Status,
			"decision_reason":             record.DecisionReason,
			"reviewed_by_discord_user_id": record.ReviewedByDiscordUserID,
			"reviewed_at":                 record.ReviewedAt,
			"version":                     record.Version,
			"updated_at":                  now,
		})
		if result.Error != nil {
			return fmt.Errorf("save appeal: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return quack.ErrAppealStateConflict
		}
		return appendAppealEvent(tx, record.model(), step, now)
	})
	if err != nil {
		return nil, err
	}
	appeal := record.model()
	return &appeal, nil
}

// appendAppealEvent adds the step's event to the appeal's timeline and queues
// its notification and audit entry. The notification is keyed to the event,
// so each event notifies at most once.
func appendAppealEvent(tx *gorm.DB, appeal quack.Appeal, step appealStep, now time.Time) error {
	event, notification := step.event, step.notification
	event.AppealID, event.GuildID = appeal.ID, appeal.GuildID
	event.MetadataJSON = cmp.Or(event.MetadataJSON, "{}")
	stamp(&event.ULIDModel, now)
	eventRecord := newAppealEventRecord(event)
	if err := tx.Create(&eventRecord).Error; err != nil {
		return fmt.Errorf("create appeal event: %w", err)
	}
	notification.AppealID, notification.EventID, notification.GuildID = appeal.ID, event.ID, appeal.GuildID
	stamp(&notification.ULIDModel, now)
	notificationRecord := newAppealNotificationRecord(notification)
	if err := tx.Create(&notificationRecord).Error; err != nil {
		return fmt.Errorf("queue appeal notification: %w", err)
	}
	return writeAudit(tx, &step.audit, appeal.ID, now)
}

func newAppealRecord(a quack.Appeal) appealRecord {
	return appealRecord{
		ID:                      a.ID,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
		GuildID:                 a.GuildID,
		CaseID:                  a.CaseID,
		TargetDiscordUserID:     a.TargetDiscordUserID,
		Status:                  a.Status,
		Content:                 a.Content,
		QuestionSnapshotJSON:    a.QuestionSnapshotJSON,
		AnswersJSON:             a.AnswersJSON,
		Version:                 a.Version,
		DecisionReason:          a.DecisionReason,
		ReviewedByDiscordUserID: a.ReviewedByDiscordUserID,
		ReviewedAt:              a.ReviewedAt,
		ReviewMessageDiscordID:  a.ReviewMessageDiscordID,
		MetadataJSON:            a.MetadataJSON,
	}
}

func (r appealRecord) model() quack.Appeal {
	return quack.Appeal{
		ULIDModel:               ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                 r.GuildID,
		CaseID:                  r.CaseID,
		TargetDiscordUserID:     r.TargetDiscordUserID,
		Status:                  r.Status,
		Content:                 r.Content,
		QuestionSnapshotJSON:    r.QuestionSnapshotJSON,
		AnswersJSON:             r.AnswersJSON,
		Version:                 r.Version,
		DecisionReason:          r.DecisionReason,
		ReviewedByDiscordUserID: r.ReviewedByDiscordUserID,
		ReviewedAt:              r.ReviewedAt,
		ReviewMessageDiscordID:  r.ReviewMessageDiscordID,
		MetadataJSON:            r.MetadataJSON,
	}
}

func newAppealEventRecord(e quack.AppealEvent) appealEventRecord {
	return appealEventRecord{
		ID:                 e.ID,
		CreatedAt:          e.CreatedAt,
		UpdatedAt:          e.UpdatedAt,
		AppealID:           e.AppealID,
		GuildID:            e.GuildID,
		EventType:          e.EventType,
		ActorDiscordUserID: e.ActorDiscordUserID,
		ActorType:          e.ActorType,
		Body:               e.Body,
		MetadataJSON:       e.MetadataJSON,
	}
}

func (r appealEventRecord) model() quack.AppealEvent {
	return quack.AppealEvent{
		ULIDModel:          ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		AppealID:           r.AppealID,
		GuildID:            r.GuildID,
		EventType:          r.EventType,
		ActorDiscordUserID: r.ActorDiscordUserID,
		ActorType:          r.ActorType,
		Body:               r.Body,
		MetadataJSON:       r.MetadataJSON,
	}
}
