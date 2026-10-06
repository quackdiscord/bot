package store

import (
	"context"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SaveCasePublication records a posted public message with a refresh
// requested. Saving one that exists does nothing, so the first presentation
// and any refresh progress are kept.
func (s *Store) SaveCasePublication(ctx context.Context, publication quack.CasePublication) error {
	now := time.Now().UTC()
	record := casePublicationRecord{
		MessageID:        publication.MessageID,
		CreatedAt:        now,
		UpdatedAt:        now,
		CaseID:           publication.CaseID,
		ChannelID:        publication.ChannelID,
		PresentationJSON: publication.PresentationJSON,
		RefreshRequested: true,
		RetryAt:          publication.RetryAt.UTC(),
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error; err != nil {
		return fmt.Errorf("save case publication: %w", err)
	}
	return nil
}

// ListDueCasePublications returns up to limit publications with a refresh
// requested and due at now, oldest first. Final publications are skipped
// until a change to their case requests another refresh.
func (s *Store) ListDueCasePublications(ctx context.Context, now time.Time, limit int) ([]quack.CasePublication, error) {
	var records []casePublicationRecord
	if err := s.db.WithContext(ctx).
		Where("refresh_requested = ? AND retry_at <= ?", true, now.UTC()).
		Order("retry_at ASC, message_id ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list due case publications: %w", err)
	}
	return modelsOf(records, casePublicationRecord.model), nil
}

// CompleteCasePublicationRefresh records a finished refresh, but only if the
// publication is still at the revision the refresh read. Otherwise the case
// changed meanwhile: the newer request stays, and the digest is cleared and
// the revision bumped so the next refresh rewrites the message even if it
// renders the same as before, since a stale edit may have landed after it.
func (s *Store) CompleteCasePublicationRefresh(ctx context.Context, params quack.CompleteCasePublicationRefreshParams) error {
	now := time.Now().UTC()
	db := s.db.WithContext(ctx)
	result := db.Model(&casePublicationRecord{}).
		Where("message_id = ? AND revision = ?", params.MessageID, params.Revision).
		Updates(map[string]any{
			"last_digest":       params.Digest,
			"retry_at":          params.RetryAt.UTC(),
			"refresh_requested": params.RefreshRequested,
			"updated_at":        now,
		})
	if result.Error != nil || result.RowsAffected > 0 {
		return wrap("complete case publication refresh", result.Error)
	}
	err := db.Model(&casePublicationRecord{}).
		Where("message_id = ? AND revision <> ?", params.MessageID, params.Revision).
		Updates(requestRefresh(now)).Error
	return wrap("complete stale case publication refresh", err)
}

// DeleteCasePublication stops tracking a publication whose message or case
// is gone.
func (s *Store) DeleteCasePublication(ctx context.Context, messageID string) error {
	err := s.db.WithContext(ctx).Where("message_id = ?", messageID).Delete(&casePublicationRecord{}).Error
	return wrap("delete case publication", err)
}

// requestPublicationRefresh marks every publication of a case due now. It
// runs inside the transaction that changes the case, so a committed change
// never loses its refresh. Publications are recorded already requested, so
// a case without any needs nothing.
func requestPublicationRefresh(tx *gorm.DB, caseID string, now time.Time) error {
	err := tx.Model(&casePublicationRecord{}).Where("case_id = ?", caseID).Updates(requestRefresh(now)).Error
	return wrap("request case publication refresh", err)
}

// requestRefresh is the update that makes a publication due now. It clears
// the digest because an in-flight edit may still change the message after
// the case returns to what was last displayed.
func requestRefresh(now time.Time) map[string]any {
	return map[string]any{
		"refresh_requested": true,
		"last_digest":       "",
		"retry_at":          now,
		"revision":          gorm.Expr("revision + 1"),
		"updated_at":        now,
	}
}

func (r casePublicationRecord) model() quack.CasePublication {
	return quack.CasePublication{
		MessageID:        r.MessageID,
		ChannelID:        r.ChannelID,
		CaseID:           r.CaseID,
		PresentationJSON: r.PresentationJSON,
		LastDigest:       r.LastDigest,
		RetryAt:          r.RetryAt,
		RefreshRequested: r.RefreshRequested,
		Revision:         r.Revision,
	}
}
