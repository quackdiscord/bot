package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// ListExecutableCaseIDs returns a bounded guild-fair executable-case batch.
// Every active guild receives one slot before a busy guild receives another,
// and the cursor rotates across calls when the batch is smaller than the guild
// count. ClaimNextCaseAction remains the authoritative transactional fence.
func (s *Store) ListExecutableCaseIDs(ctx context.Context, limit int) ([]string, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if limit <= 0 {
		limit = 100
	}

	s.executableMu.Lock()
	defer s.executableMu.Unlock()

	now := time.Now().UTC()
	var rows []struct {
		CaseID  string
		GuildID string
	}
	query := `
WITH executable_cases AS (
    SELECT e.case_id,
           c.guild_id,
           MIN(e.position) AS first_position,
           MIN(COALESCE(e.next_retry_at, e.lease_expires_at, e.created_at)) AS ready_at
      FROM case_action_executions AS e
      JOIN cases AS c ON c.id = e.case_id
     WHERE ((e.status IN (?, ?) AND (e.next_retry_at IS NULL OR e.next_retry_at <= ?))
            OR (e.status = ? AND e.lease_expires_at <= ?))
     GROUP BY e.case_id, c.guild_id
), ranked_cases AS (
    SELECT case_id,
           guild_id,
           first_position,
           ready_at,
           ROW_NUMBER() OVER (
               PARTITION BY guild_id
               ORDER BY first_position ASC, ready_at ASC, case_id ASC
           ) AS guild_rank
      FROM executable_cases
)
SELECT case_id, guild_id
  FROM ranked_cases
 ORDER BY guild_rank ASC,
          CASE WHEN guild_id > ? THEN 0 ELSE 1 END ASC,
          guild_id ASC,
          first_position ASC,
          ready_at ASC,
          case_id ASC
 LIMIT ?`
	if err := s.db.WithContext(ctx).Raw(query,
		quack.ActionExecutionPending,
		quack.ActionExecutionRetrying,
		now,
		quack.ActionExecutionRunning,
		now,
		s.executableGuildCursor,
		limit,
	).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list executable case ids: %w", err)
	}

	caseIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		caseIDs = append(caseIDs, row.CaseID)
	}
	if len(rows) > 0 {
		s.executableGuildCursor = rows[len(rows)-1].GuildID
	}
	return caseIDs, nil
}
