package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// queueVoidedCaseReversals queues a reversal of each succeeded timeout and
// ban of a voided case that has none yet, so voiding undoes what the case
// did. The voider is recorded as the requester, and the reversal checks
// their permission when it runs. Kicks cannot be undone, and failed actions
// stay in the review queue since their outcome is uncertain. The caller
// holds the case lock.
func queueVoidedCaseReversals(tx *gorm.DB, c caseRecord, appealID *string, now time.Time) error {
	if c.Validity != quack.CaseValidityVoided {
		return nil
	}
	var originals []executionRecord
	if err := tx.Where("case_id = ? AND status = ? AND reversal_of_execution_id IS NULL AND action_type IN ?",
		c.ID, quack.ActionExecutionSucceeded, []quack.ActionType{quack.ActionTimeoutUser, quack.ActionBanUser}).
		Order("position ASC").Find(&originals).Error; err != nil {
		return fmt.Errorf("find voided case actions to reverse: %w", err)
	}
	if len(originals) == 0 {
		return nil
	}
	if appealID == nil {
		var appeal appealRecord
		found, err := first(tx.Select("id").Where("case_id = ? AND status = ?", c.ID, quack.AppealStatusAccepted), &appeal)
		if err != nil {
			return fmt.Errorf("find accepted appeal: %w", err)
		}
		if found {
			appealID = &appeal.ID
		}
	}
	for _, original := range originals {
		inverse := quack.ActionRemoveTimeout
		if original.ActionType == quack.ActionBanUser {
			inverse = quack.ActionUnbanUser
		}
		_, err := queueReversal(tx, c, original, quack.QueueCaseReversalParams{
			GuildID:             c.GuildID,
			CaseID:              c.ID,
			ActorDiscordUserID:  c.VoidedByDiscordUserID,
			OriginalExecutionID: original.ID,
			AppealID:            appealID,
			ActionType:          inverse,
		}, jsonObject(map[string]any{"requested_by": c.VoidedByDiscordUserID}), now)
		if err != nil {
			return err
		}
	}
	return nil
}

// reversalNoop reports whether a succeeded reversal found the punishment
// already over, as the Discord adapter flags with "reversal_noop" in its
// response.
func reversalNoop(e executionRecord, params quack.CompleteCaseActionParams) bool {
	if e.ReversalOfExecutionID == nil || params.ExecutionStatus != quack.ActionExecutionSucceeded {
		return false
	}
	var response struct {
		ReversalNoop bool `json:"reversal_noop"`
	}
	return json.Unmarshal([]byte(params.ResponsePayloadJSON), &response) == nil && response.ReversalNoop
}

// CompetingPunishmentExists reports whether another execution of the same
// kind against the same member may still be in effect: one queued or
// running, or one that succeeded or failed (with an uncertain outcome) after
// the original started. It is a conservative check made just before an
// automatic reversal, not a lock held across the Discord call.
func (s *Store) CompetingPunishmentExists(ctx context.Context, guildID, caseID, originalExecutionID string) (bool, error) {
	db := s.db.WithContext(ctx)
	var c caseRecord
	found, err := first(db.Where("id = ? AND guild_id = ?", caseID, guildID), &c)
	if err != nil || !found {
		return false, wrap("get case for reversal check", err)
	}
	var original executionRecord
	found, err = first(db.Where("id = ? AND case_id = ?", originalExecutionID, caseID), &original)
	if err != nil || !found {
		return false, wrap("get original execution", err)
	}
	since := original.CreatedAt
	if original.StartedAt != nil {
		since = *original.StartedAt
	}
	var competing int64
	err = db.Model(&executionRecord{}).
		Where("id <> ? AND action_type = ? AND reversal_of_execution_id IS NULL", original.ID, original.ActionType).
		Where("case_id IN (SELECT id FROM cases WHERE guild_id = ? AND target_discord_user_id = ?)", guildID, c.TargetDiscordUserID).
		Where("status IN ? OR (status IN ? AND (created_at >= ? OR started_at >= ?))",
			activeExecutionStatuses,
			[]quack.ActionExecutionStatus{quack.ActionExecutionSucceeded, quack.ActionExecutionFailed}, since, since).
		Count(&competing).Error
	if err != nil {
		return false, fmt.Errorf("check competing punishments: %w", err)
	}
	return competing > 0, nil
}
