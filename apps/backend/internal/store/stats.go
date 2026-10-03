package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// DeriveStaffStatistics counts a guild's cases, executions, appeals, and
// audit entries created in [From, To), broken down by day and by category.
// Every count is a SQL aggregate, so the cost does not grow with row size.
func (s *Store) DeriveStaffStatistics(ctx context.Context, params quack.StaffStatisticsParams) (*quack.StaffStatistics, error) {
	if params.GuildID == "" || params.From.IsZero() || params.To.IsZero() || !params.From.Before(params.To) {
		return nil, errors.New("invalid staff statistics range")
	}
	db := s.db.WithContext(ctx)
	from, to := params.From.UTC(), params.To.UTC()
	inRange := func(model any, guildFilter string) func() *gorm.DB {
		return func() *gorm.DB {
			return db.Model(model).Where(guildFilter, params.GuildID).
				Where("created_at >= ? AND created_at < ?", from, to)
		}
	}
	cases := inRange(&caseRecord{}, "guild_id = ?")
	actions := inRange(&executionRecord{}, "case_id IN (SELECT id FROM cases WHERE guild_id = ?)")
	appeals := inRange(&appealRecord{}, "guild_id = ?")
	audits := inRange(&auditRecord{}, "guild_id = ?")
	day := dayExpression(db)

	result := &quack.StaffStatistics{From: from, To: to}
	for _, breakdown := range []struct {
		query  func() *gorm.DB
		expr   string
		into   *[]quack.StatisticBucket
		total  *int64
		source string
	}{
		{cases, day, &result.CasesByDay, &result.CaseTotal, "case"},
		{cases, "COALESCE(NULLIF(template_id, ''), 'historical_or_deleted')", &result.CasesByTemplate, nil, "case"},
		{cases, "validity", &result.CasesByValidity, nil, "case"},
		{cases, "source", &result.CasesBySource, nil, "case"},
		{actions, day, &result.ActionsByDay, &result.ActionTotal, "action"},
		{actions, "action_type", &result.ActionsByType, nil, "action"},
		{actions, "status", &result.ActionsByResult, nil, "action"},
		{appeals, day, &result.AppealsByDay, &result.AppealTotal, "appeal"},
		{appeals, "status", &result.AppealsByStatus, nil, "appeal"},
		{audits, day, &result.AuditsByDay, &result.AuditTotal, "audit"},
		{audits, "action", &result.AuditsByAction, nil, "audit"},
		{audits, "result", &result.AuditsByResult, nil, "audit"},
		{audits, "source", &result.AuditsBySource, nil, "audit"},
	} {
		buckets := []quack.StatisticBucket{}
		err := breakdown.query().
			Select(breakdown.expr + " AS `key`, COUNT(*) AS count").
			Group("`key`").Order("`key`").
			Scan(&buckets).Error
		if err != nil {
			return nil, fmt.Errorf("derive %s statistics: %w", breakdown.source, err)
		}
		*breakdown.into = buckets
		if breakdown.total != nil {
			for _, bucket := range buckets {
				*breakdown.total += bucket.Count
			}
		}
	}
	return result, nil
}

// dayExpression formats created_at as a UTC calendar day (YYYY-MM-DD).
// Timestamps are stored in UTC, so no zone conversion is needed.
func dayExpression(db *gorm.DB) string {
	if db.Dialector.Name() == "mysql" {
		return "DATE_FORMAT(created_at, '%Y-%m-%d')"
	}
	return "strftime('%Y-%m-%d', created_at)"
}
