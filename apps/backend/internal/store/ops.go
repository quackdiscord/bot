package store

import (
	"context"
	"fmt"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// ActionQueueSnapshot summarizes the stored executions of one guild, or of
// every guild when guildID is empty: counts by status, the longest-waiting
// queued execution, and up to failureLimit recent failures.
func (s *Store) ActionQueueSnapshot(ctx context.Context, guildID string, failureLimit int) (*quack.ActionQueueSnapshot, error) {
	if failureLimit <= 0 {
		failureLimit = 10
	}
	failureLimit = min(failureLimit, 25)
	executions := func() *gorm.DB {
		query := s.db.WithContext(ctx).Table("case_action_executions AS e").Joins("JOIN cases AS c ON c.id = e.case_id")
		if guildID != "" {
			query = query.Where("c.guild_id = ?", guildID)
		}
		return query
	}

	snapshot := &quack.ActionQueueSnapshot{}
	if err := executions().Select("e.status AS status, COUNT(*) AS count").
		Group("e.status").Scan(&snapshot.StatusCounts).Error; err != nil {
		return nil, fmt.Errorf("count action executions by status: %w", err)
	}
	var oldest []quack.OldestActionExecution
	if err := executions().
		Select("e.id, e.case_id, c.case_number, e.action_type, e.status, e.created_at, e.next_retry_at").
		Where("e.status IN ?", []quack.ActionExecutionStatus{quack.ActionExecutionPending, quack.ActionExecutionRetrying}).
		Order("COALESCE(e.next_retry_at, e.created_at) ASC").Limit(1).
		Scan(&oldest).Error; err != nil {
		return nil, fmt.Errorf("get oldest queued action: %w", err)
	}
	if len(oldest) > 0 {
		snapshot.OldestPendingOrRetry = &oldest[0]
	}
	if err := executions().
		Select("e.id, e.case_id, c.case_number, e.action_type, e.status, e.last_error_code, e.last_error, e.updated_at").
		Where("e.status = ? OR e.last_error_code <> ''", quack.ActionExecutionFailed).
		Order("e.updated_at DESC").Limit(failureLimit).
		Scan(&snapshot.RecentFailures).Error; err != nil {
		return nil, fmt.Errorf("list recent action failures: %w", err)
	}
	return snapshot, nil
}

// OperationalMetricSnapshot returns process-wide counters for the metrics
// endpoint. They are totals only, never per guild or member, so they leak
// nothing about who is moderated.
func (s *Store) OperationalMetricSnapshot(ctx context.Context) (map[string]int64, error) {
	counters := []struct {
		metric, table, where string
		args                 []any
	}{
		{"quack_cases_total", "cases", "", nil},
		{"quack_action_attempts_total", "case_action_attempts", "", nil},
		{"quack_notifications_total", "case_notifications", "", nil},
		{"quack_appeals_total", "appeals", "", nil},
		{"quack_audit_events_total", "audit_log_entries", "", nil},
		{"quack_action_failures_total", "case_action_executions", "status = ?", []any{quack.ActionExecutionFailed}},
		{"quack_action_retries_total", "case_action_attempts", "attempt_number > ?", []any{1}},
		{"quack_audit_mirror_events_total", "audit_log_entries", "action LIKE ?", []any{"audit_mirror.%"}},
		{"quack_audit_mirror_deliveries_total", "audit_mirror_deliveries", "status = ?", []any{quack.AuditMirrorDelivered}},
		{"quack_audit_mirror_failures_total", "audit_mirror_deliveries", "status = ?", []any{quack.AuditMirrorFailed}},
		{"quack_optional_module_events_total", "audit_log_entries", "action LIKE ? OR action LIKE ? OR action LIKE ?",
			[]any{"ticket.%", "general_logging.%", "honeypot.%"}},
	}
	result := make(map[string]int64, len(counters))
	for _, counter := range counters {
		query := s.db.WithContext(ctx).Table(counter.table)
		if counter.where != "" {
			query = query.Where(counter.where, counter.args...)
		}
		var count int64
		if err := query.Count(&count).Error; err != nil {
			return nil, fmt.Errorf("count %s: %w", counter.metric, err)
		}
		result[counter.metric] = count
	}
	return result, nil
}
