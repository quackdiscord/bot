package store

import (
	"context"
	"fmt"
	"strconv"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CountTemplateCasesForTarget counts a member's valid cases under one
// template, across all of its versions. Imported v4 cases are history only
// and never count toward escalation.
func (s *Store) CountTemplateCasesForTarget(ctx context.Context, params quack.CountTemplateCasesForTargetParams) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&caseRecord{}).
		Where("guild_id = ? AND target_discord_user_id = ? AND template_id = ?",
			params.GuildID, params.TargetDiscordUserID, params.TemplateID).
		Where("validity = ? AND source <> ?", quack.CaseValidityValid, quack.CaseSourceV4Import).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count template cases for target: %w", err)
	}
	return count, nil
}

// ListCases returns every case in a guild in case-number order.
func (s *Store) ListCases(ctx context.Context, guildID string) ([]quack.Case, error) {
	var records []caseRecord
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).
		Order("case_number ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	return modelsOf(records, caseRecord.model), nil
}

// ListCasesFiltered returns a page of a guild's cases, newest first, and the
// number of cases matching the filters.
func (s *Store) ListCasesFiltered(ctx context.Context, params quack.ListCasesParams) (*quack.ListCasesResult, error) {
	db := s.db.WithContext(ctx)
	limit, offset := page(params.Limit, params.Offset)
	var total int64
	if err := filterCases(db.Model(&caseRecord{}), params).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count cases: %w", err)
	}
	var records []caseRecord
	if err := filterCases(db.Model(&caseRecord{}), params).
		Order("case_number DESC").Limit(limit).Offset(offset).Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}
	return &quack.ListCasesResult{Cases: modelsOf(records, caseRecord.model), Total: total}, nil
}

// filterCases applies the non-empty filters in params.
func filterCases(query *gorm.DB, params quack.ListCasesParams) *gorm.DB {
	query = query.Where("guild_id = ?", params.GuildID)
	for _, filter := range [][2]string{
		{"target_discord_user_id = ?", params.TargetDiscordUserID},
		{"moderator_discord_user_id = ?", params.ModeratorDiscordUserID},
		{"template_id = ?", params.TemplateID},
		{"validity = ?", string(params.Validity)},
		{"case_number = ?", params.CaseNumber},
		{"created_at >= ?", params.CreatedAfter},
		{"created_at <= ?", params.CreatedBefore},
		{"EXISTS (SELECT 1 FROM case_action_executions e WHERE e.case_id = cases.id AND e.status = ?)", params.ActionResult},
		{"EXISTS (SELECT 1 FROM appeals a WHERE a.case_id = cases.id AND a.status = ?)", params.AppealStatus},
	} {
		if filter[1] != "" {
			query = query.Where(filter[0], filter[1])
		}
	}
	return query
}

// GetCaseByIDOrNumber resolves ref as a guild case number when it is a
// canonical decimal number, and as a case ID otherwise. It returns nil when
// nothing matches.
func (s *Store) GetCaseByIDOrNumber(ctx context.Context, guildID, ref string) (*quack.Case, error) {
	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	if number, err := strconv.ParseUint(ref, 10, 64); err == nil && strconv.FormatUint(number, 10) == ref {
		query = query.Where("case_number = ?", number)
	} else {
		query = query.Where("id = ?", ref)
	}
	return findOne(query, "get case", caseRecord.model)
}

// GetCaseByID returns a case in any guild, or nil. Callers authorize access
// themselves, for example by checking the case's target member.
func (s *Store) GetCaseByID(ctx context.Context, caseID string) (*quack.Case, error) {
	return findOne(s.db.WithContext(ctx).Where("id = ?", caseID), "get case", caseRecord.model)
}

// GetCaseByIdempotencyKey returns the case an earlier request with the same
// Idempotency-Key created, or nil.
func (s *Store) GetCaseByIdempotencyKey(ctx context.Context, guildID, key string) (*quack.Case, error) {
	query := s.db.WithContext(ctx).Where("guild_id = ? AND idempotency_key = ?", guildID, key)
	return findOne(query, "get case", caseRecord.model)
}

// TargetCaseSummary counts all of a member's cases in a guild, in total, by
// validity, and by template. Cases without a template count under "".
func (s *Store) TargetCaseSummary(ctx context.Context, guildID, targetDiscordUserID string) (*quack.TargetCaseSummary, error) {
	cases := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&caseRecord{}).
			Where("guild_id = ? AND target_discord_user_id = ?", guildID, targetDiscordUserID)
	}
	summary := &quack.TargetCaseSummary{
		ByValidity: map[quack.CaseValidity]int64{},
		ByTemplate: map[string]int64{},
	}
	var rows []struct {
		Bucket string
		Count  int64
	}
	if err := cases().Select("validity AS bucket, COUNT(*) AS count").
		Group("validity").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count target cases by validity: %w", err)
	}
	for _, row := range rows {
		summary.ByValidity[quack.CaseValidity(row.Bucket)] = row.Count
		summary.Total += row.Count
	}
	rows = nil
	if err := cases().Select("COALESCE(template_id, '') AS bucket, COUNT(*) AS count").
		Group("template_id").Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count target cases by template: %w", err)
	}
	for _, row := range rows {
		summary.ByTemplate[row.Bucket] += row.Count
	}
	return summary, nil
}

// ListCaseEvents returns a case timeline, oldest first. Callers authorize
// the case first.
func (s *Store) ListCaseEvents(ctx context.Context, caseID string) ([]quack.CaseEvent, error) {
	var records []caseEventRecord
	if err := s.db.WithContext(ctx).Where("case_id = ?", caseID).
		Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case events: %w", err)
	}
	return modelsOf(records, caseEventRecord.model), nil
}

// ListCaseActionExecutions returns a case's executions in run order.
func (s *Store) ListCaseActionExecutions(ctx context.Context, caseID string) ([]quack.CaseActionExecution, error) {
	return s.ListCaseActionsForCases(ctx, []string{caseID})
}

// ListCaseActionsForCases returns the executions of a page of cases. Callers
// pass only case IDs they have already authorized.
func (s *Store) ListCaseActionsForCases(ctx context.Context, caseIDs []string) ([]quack.CaseActionExecution, error) {
	if len(caseIDs) == 0 {
		return []quack.CaseActionExecution{}, nil
	}
	var records []executionRecord
	if err := s.db.WithContext(ctx).Where("case_id IN ?", caseIDs).
		Order("case_id ASC, position ASC, id ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case action executions: %w", err)
	}
	return modelsOf(records, executionRecord.model), nil
}

// ListCaseActionAttempts returns the attempts of authorized executions in
// attempt order.
func (s *Store) ListCaseActionAttempts(ctx context.Context, executionIDs []string) ([]quack.CaseActionAttempt, error) {
	if len(executionIDs) == 0 {
		return nil, nil
	}
	var records []attemptRecord
	if err := s.db.WithContext(ctx).Where("execution_id IN ?", executionIDs).
		Order("execution_id ASC, attempt_number ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case action attempts: %w", err)
	}
	return modelsOf(records, attemptRecord.model), nil
}

// GetCaseActionExecution returns one of a guild's executions, or nil.
func (s *Store) GetCaseActionExecution(ctx context.Context, guildID, executionID string) (*quack.CaseActionExecution, error) {
	query := s.db.WithContext(ctx).
		Where("id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", executionID, guildID)
	return findOne(query, "get case action execution", executionRecord.model)
}

// ListCaseEvidence returns a case's evidence snapshots and their
// attachments, oldest first.
func (s *Store) ListCaseEvidence(ctx context.Context, caseID string) ([]quack.CaseEvidenceSnapshot, []quack.CaseEvidenceAttachment, error) {
	db := s.db.WithContext(ctx)
	var snapshots []evidenceRecord
	if err := db.Where("case_id = ?", caseID).Order("created_at ASC, id ASC").Find(&snapshots).Error; err != nil {
		return nil, nil, fmt.Errorf("list case evidence: %w", err)
	}
	evidence := modelsOf(snapshots, evidenceRecord.model)
	if len(snapshots) == 0 {
		return evidence, nil, nil
	}
	ids := make([]string, len(snapshots))
	for i, r := range snapshots {
		ids[i] = r.ID
	}
	var records []attachmentRecord
	if err := db.Where("evidence_id IN ?", ids).Order("created_at ASC, id ASC").Find(&records).Error; err != nil {
		return nil, nil, fmt.Errorf("list evidence attachments: %w", err)
	}
	return evidence, modelsOf(records, attachmentRecord.model), nil
}

// GetCaseNotification returns a case's member notification, or nil when the
// selected level does not notify.
func (s *Store) GetCaseNotification(ctx context.Context, caseID string) (*quack.CaseNotification, error) {
	query := s.db.WithContext(ctx).Where("case_id = ?", caseID)
	return findOne(query, "get case notification", caseNotificationRecord.model)
}
