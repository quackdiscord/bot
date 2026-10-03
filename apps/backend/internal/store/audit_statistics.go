package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// ListPendingAuditMirrorEntries returns important immutable entries without a successful mirror outcome.
func (s *Store) ListPendingAuditMirrorEntries(ctx context.Context, limit int) ([]quack.AuditLogEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var entries []quack.AuditLogEntry
	retryAfter := time.Now().UTC().Add(-time.Minute)
	err := s.db.WithContext(ctx).
		Where("action IN ?", quack.ImportantAuditActions()).
		Where("NOT EXISTS (SELECT 1 FROM audit_log_entries outcomes WHERE outcomes.guild_id = audit_log_entries.guild_id AND outcomes.action IN ? AND outcomes.resource_type = ? AND outcomes.resource_id = audit_log_entries.id AND outcomes.result = ?)", []string{string(quack.AuditActionMirrorDelivered), string(quack.AuditActionMirrorSkipped)}, "audit_entry", quack.AuditResultSuccess).
		Where("NOT EXISTS (SELECT 1 FROM audit_log_entries failures WHERE failures.guild_id = audit_log_entries.guild_id AND failures.action = ? AND failures.resource_type = ? AND failures.resource_id = audit_log_entries.id AND failures.created_at > ?)", string(quack.AuditActionMirrorFailed), "audit_entry", retryAfter).
		Order("created_at ASC, id ASC").Limit(limit).Find(&entries).Error
	if err != nil {
		return nil, fmt.Errorf("list pending audit mirror entries: %w", err)
	}
	return entries, nil
}

// DeriveStaffStatistics calculates operational counts directly from immutable source records.
func (s *Store) DeriveStaffStatistics(ctx context.Context, params quack.StaffStatisticsParams) (*quack.StaffStatistics, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if params.GuildID == "" || params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("invalid staff statistics range")
	}

	var cases []quack.Case
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID), params).Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("derive case statistics: %w", err)
	}
	var actions []quack.CaseActionExecution
	if err := timeRange(s.db.WithContext(ctx).Where("case_id IN (SELECT id FROM cases WHERE guild_id = ?)", params.GuildID), params).Find(&actions).Error; err != nil {
		return nil, fmt.Errorf("derive action statistics: %w", err)
	}
	var appeals []quack.Appeal
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID), params).Find(&appeals).Error; err != nil {
		return nil, fmt.Errorf("derive appeal statistics: %w", err)
	}
	var audits []quack.AuditLogEntry
	if err := timeRange(s.db.WithContext(ctx).Where("guild_id = ?", params.GuildID), params).Find(&audits).Error; err != nil {
		return nil, fmt.Errorf("derive audit statistics: %w", err)
	}

	result := &quack.StaffStatistics{From: params.From.UTC(), To: params.To.UTC(), CaseTotal: int64(len(cases)), ActionTotal: int64(len(actions)), AppealTotal: int64(len(appeals)), AuditTotal: int64(len(audits))}
	caseDays, caseTemplates, caseValidity, caseSources := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range cases {
		caseDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		template := "historical_or_deleted"
		if item.TemplateID != nil && *item.TemplateID != "" {
			template = *item.TemplateID
		}
		caseTemplates[template]++
		caseValidity[string(item.Validity)]++
		caseSources[string(item.Source)]++
	}
	actionDays, actionTypes, actionResults := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range actions {
		actionDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		actionTypes[string(item.ActionType)]++
		actionResults[string(item.Status)]++
	}
	appealDays, appealStatuses := map[string]int64{}, map[string]int64{}
	for _, item := range appeals {
		appealDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		appealStatuses[string(item.Status)]++
	}
	auditDays, auditActions, auditResults, auditSources := map[string]int64{}, map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, item := range audits {
		auditDays[item.CreatedAt.UTC().Format(time.DateOnly)]++
		auditActions[item.Action]++
		auditResults[string(item.Result)]++
		auditSources[string(item.Source)]++
	}
	result.CasesByDay = statisticBuckets(caseDays)
	result.CasesByTemplate = statisticBuckets(caseTemplates)
	result.CasesByValidity = statisticBuckets(caseValidity)
	result.CasesBySource = statisticBuckets(caseSources)
	result.ActionsByDay = statisticBuckets(actionDays)
	result.ActionsByType = statisticBuckets(actionTypes)
	result.ActionsByResult = statisticBuckets(actionResults)
	result.AppealsByDay = statisticBuckets(appealDays)
	result.AppealsByStatus = statisticBuckets(appealStatuses)
	result.AuditsByDay = statisticBuckets(auditDays)
	result.AuditsByAction = statisticBuckets(auditActions)
	result.AuditsByResult = statisticBuckets(auditResults)
	result.AuditsBySource = statisticBuckets(auditSources)
	return result, nil
}

func timeRange(query *gorm.DB, params quack.StaffStatisticsParams) *gorm.DB {
	return query.Where("created_at >= ? AND created_at < ?", params.From.UTC(), params.To.UTC())
}

func statisticBuckets(counts map[string]int64) []quack.StatisticBucket {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]quack.StatisticBucket, 0, len(keys))
	for _, key := range keys {
		result = append(result, quack.StatisticBucket{Key: key, Count: counts[key]})
	}
	return result
}
