package honeypot

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// cleanupLease is how long one delete attempt owns its message.
	cleanupLease = 30 * time.Second
	// cleanupTimeout bounds one Discord delete.
	cleanupTimeout = 10 * time.Second
	// maxCleanupBatch caps the messages one ProcessCleanups call deletes.
	maxCleanupBatch = 25
)

// MessageCleanup is one bait message waiting to be deleted. Every non-exempt
// message in a burst gets one, linked to the incident it joined. It becomes
// due only once that incident has a saved case, so a failed or pending case
// keeps its source messages. Completed rows stay, so an old gateway replay
// of the message is recognized and never opens another case.
type MessageCleanup struct {
	ID                  string    `gorm:"type:char(26);primaryKey"`
	GuildID             string    `gorm:"type:char(26);not null;uniqueIndex:idx_honeypot_cleanup_message,priority:1"`
	MessageDiscordID    string    `gorm:"size:32;not null;uniqueIndex:idx_honeypot_cleanup_message,priority:2"`
	ChannelDiscordID    string    `gorm:"size:32;not null"`
	TargetDiscordUserID string    `gorm:"size:32;not null"`
	TriggerID           string    `gorm:"type:char(26);not null;index"`
	AttemptCount        uint      `gorm:"not null"`
	NextAttemptAt       time.Time `gorm:"not null;index"`
	CompletedAt         *time.Time
	CreatedAt           time.Time `gorm:"not null"`
}

// TableName returns the honeypot_message_cleanups table.
func (MessageCleanup) TableName() string { return "honeypot_message_cleanups" }

// ProcessCleanups deletes up to limit due bait messages, one lease at a
// time so a slow Discord call cannot expire the leases of the rest. A failed
// delete backs off and stays due; an interrupted one is retried once its
// lease expires. It does nothing unless the applier is a MessageCleaner.
func (s *Service) ProcessCleanups(ctx context.Context, limit int) error {
	cleaner, ok := s.applier.(MessageCleaner)
	if !ok {
		return nil
	}
	var failures []error
	for range min(max(limit, 1), maxCleanupBatch) {
		message, err := s.store.claimCleanup(ctx)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if message == nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, cleanupTimeout)
		deleteErr := cleaner.DeleteHoneypotMessage(attemptCtx, message.ChannelDiscordID, message.MessageDiscordID)
		cancel()
		if err := s.store.finishCleanup(ctx, *message, deleteErr == nil); err != nil {
			failures = append(failures, err)
		}
		if deleteErr != nil {
			failures = append(failures, deleteErr)
		}
	}
	return errors.Join(failures...)
}

// scheduleCleanup queues message for deletion under triggerID. It runs in
// the incident claim's transaction, so every accepted message is queued even
// if Quack stops before the case is opened.
func (s *Store) scheduleCleanup(ctx context.Context, message Message, triggerID string) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&MessageCleanup{
		ID:                  ulid.Make().String(),
		GuildID:             message.GuildID,
		MessageDiscordID:    message.MessageDiscordID,
		ChannelDiscordID:    message.ChannelDiscordID,
		TargetDiscordUserID: message.AuthorDiscordUserID,
		TriggerID:           triggerID,
		NextAttemptAt:       now,
		CreatedAt:           now,
	}).Error
}

// claimCleanup leases the next due message whose incident has a saved case,
// or returns nil. The attempt count is the fence: bumping it takes the
// lease, and a stale worker's finish no longer matches.
func (s *Store) claimCleanup(ctx context.Context) (*MessageCleanup, error) {
	now := time.Now().UTC()
	var candidates []MessageCleanup
	err := s.db.WithContext(ctx).Table("honeypot_message_cleanups AS cleanup").Select("cleanup.*").
		Joins("JOIN honeypot_triggers AS incident ON incident.id = cleanup.trigger_id AND incident.guild_id = cleanup.guild_id").
		Where("cleanup.completed_at IS NULL AND cleanup.next_attempt_at <= ? AND incident.outcome = ? AND incident.case_id <> ''",
			now, OutcomeCreated).
		Order("cleanup.next_attempt_at ASC").Limit(1).Find(&candidates).Error
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	candidate := candidates[0]
	result := s.db.WithContext(ctx).Model(&MessageCleanup{}).
		Where("id = ? AND completed_at IS NULL AND next_attempt_at <= ? AND attempt_count = ?", candidate.ID, now, candidate.AttemptCount).
		Updates(map[string]any{
			"next_attempt_at": now.Add(cleanupLease),
			"attempt_count":   gorm.Expr("attempt_count + 1"),
		})
	if result.Error != nil || result.RowsAffected != 1 {
		return nil, result.Error
	}
	candidate.AttemptCount++
	return &candidate, nil
}

// finishCleanup records a delete, or schedules a retry with exponential
// backoff capped at about a minute. It changes nothing if another worker
// has since taken the lease.
func (s *Store) finishCleanup(ctx context.Context, message MessageCleanup, succeeded bool) error {
	now := time.Now().UTC()
	values := map[string]any{"next_attempt_at": now.Add(time.Second << min(message.AttemptCount-1, 6))}
	if succeeded {
		values["completed_at"] = now
	}
	return s.db.WithContext(ctx).Model(&MessageCleanup{}).
		Where("id = ? AND attempt_count = ? AND completed_at IS NULL", message.ID, message.AttemptCount).
		Updates(values).Error
}
