package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CountTemplateCasesForTarget counts non-voided cases across versions of one guild-owned template.
func (s *Store) CountTemplateCasesForTarget(ctx context.Context, params quack.CountTemplateCasesForTargetParams) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errors.New("database not connected")
	}

	query := s.db.WithContext(ctx).
		Model(&quack.Case{}).
		Where("guild_id = ?", params.GuildID).
		Where("template_id = ?", params.TemplateID).
		Where("target_discord_user_id = ?", params.TargetDiscordUserID).
		Where("status <> ?", quack.CaseValidityVoided)
	query = query.Where("source <> ?", quack.CaseSourceV4Import)
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count template cases for target: %w", err)
	}

	return count, nil
}

// ListCases returns cases subject to authorization, ordering, and filtering constraints.
func (s *Store) ListCases(ctx context.Context, guildID string) ([]quack.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var cases []quack.Case
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Order("case_number ASC").Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("list cases: %w", err)
	}

	return cases, nil
}

// ListCasesFiltered applies guild, member, and moderation filters before stable pagination.
func (s *Store) ListCasesFiltered(ctx context.Context, params quack.ListCasesParams) (*quack.ListCasesResult, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	offset := params.Offset
	if offset < 0 {
		offset = 0
	}

	var total int64
	if err := filteredCasesQuery(s.db.WithContext(ctx).Model(&quack.Case{}), params).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("count cases: %w", err)
	}

	var cases []quack.Case
	if err := filteredCasesQuery(s.db.WithContext(ctx).Model(&quack.Case{}), params).
		Order("case_number DESC").
		Limit(limit).
		Offset(offset).
		Find(&cases).Error; err != nil {
		return nil, fmt.Errorf("list filtered cases: %w", err)
	}

	return &quack.ListCasesResult{Cases: cases, Total: total}, nil
}

// GetCaseByIDOrNumber resolves either an internal case ID or a human-readable number within one guild.
func (s *Store) GetCaseByIDOrNumber(ctx context.Context, guildID, caseRef string) (*quack.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	if caseNumber, err := strconv.ParseUint(caseRef, 10, 64); err == nil && strconv.FormatUint(caseNumber, 10) == caseRef {
		query = query.Where("case_number = ?", caseNumber)
	} else {
		query = query.Where("id = ?", caseRef)
	}

	var caseModel quack.Case
	if err := query.First(&caseModel).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case by id or number: %w", err)
	}

	return &caseModel, nil
}

// GetCaseByID retrieves a case without requiring current guild membership, for member-owned access checks performed by the service.
func (s *Store) GetCaseByID(ctx context.Context, caseID string) (*quack.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var item quack.Case
	result := s.db.WithContext(ctx).Where("id = ?", caseID).First(&item)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("get case by id: %w", result.Error)
	}
	return &item, nil
}

// GetCaseByIdempotencyKey retrieves the durable result of an externally retried case request.
func (s *Store) GetCaseByIdempotencyKey(ctx context.Context, guildID, key string) (*quack.Case, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var item quack.Case
	result := s.db.WithContext(ctx).Where("guild_id = ? AND idempotency_key = ?", guildID, key).First(&item)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &item, nil
}

// quack.TargetCaseSummary encapsulates the target case summary rule so callers share one consistent package implementation.
func (s *Store) TargetCaseSummary(ctx context.Context, guildID, targetDiscordUserID string) (*quack.TargetCaseSummary, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	summary := &quack.TargetCaseSummary{
		ByValidity: map[quack.CaseValidity]int64{},
		ByTemplate: map[string]int64{},
	}

	targetCases := func() *gorm.DB {
		return s.db.WithContext(ctx).Model(&quack.Case{}).
			Where("guild_id = ?", guildID).
			Where("target_discord_user_id = ?", targetDiscordUserID)
	}
	if err := targetCases().Count(&summary.Total).Error; err != nil {
		return nil, fmt.Errorf("count target cases: %w", err)
	}

	var validityRows []struct {
		Validity quack.CaseValidity `gorm:"column:status"`
		Count    int64
	}
	if err := targetCases().Select("status, COUNT(*) AS count").Group("status").Scan(&validityRows).Error; err != nil {
		return nil, fmt.Errorf("count target cases by validity: %w", err)
	}
	for _, row := range validityRows {
		summary.ByValidity[row.Validity] = row.Count
	}

	var templateRows []struct {
		TemplateID string
		Count      int64
	}
	if err := targetCases().Select("COALESCE(template_id, '') AS template_id, COUNT(*) AS count").Group("template_id").Scan(&templateRows).Error; err != nil {
		return nil, fmt.Errorf("count target cases by template: %w", err)
	}
	for _, row := range templateRows {
		summary.ByTemplate[row.TemplateID] = row.Count
	}

	return summary, nil
}

// ListCaseEvents returns a case timeline in chronological order; the caller must authorize the case first.
func (s *Store) ListCaseEvents(ctx context.Context, caseID string) ([]quack.CaseEvent, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var events []quack.CaseEvent
	if err := s.db.WithContext(ctx).
		Where("case_id = ?", caseID).
		Where("event_type NOT IN ?", retiredCaseEventTypes).
		Order("created_at ASC").
		Find(&events).Error; err != nil {
		return nil, fmt.Errorf("list case events: %w", err)
	}

	return events, nil
}

// ListCaseActionExecutions returns case action executions subject to authorization, ordering, and filtering constraints.
func (s *Store) ListCaseActionExecutions(ctx context.Context, caseID string) ([]quack.CaseActionExecution, error) {
	return s.ListCaseActionsForCases(ctx, []string{caseID})
}

// ListCaseActionsForCases fetches actions for an already authorized case page.
// Callers supply only case IDs selected by their guild or member ownership query.
func (s *Store) ListCaseActionsForCases(ctx context.Context, caseIDs []string) ([]quack.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if len(caseIDs) == 0 {
		return []quack.CaseActionExecution{}, nil
	}

	var executions []quack.CaseActionExecution
	if err := s.db.WithContext(ctx).Where("case_id IN ?", caseIDs).Order("case_id ASC, position ASC, id ASC").Find(&executions).Error; err != nil {
		return nil, fmt.Errorf("list case action executions: %w", err)
	}

	return executions, nil
}

// ListCaseActionAttempts loads attempts for authorized execution IDs in attempt order.
func (s *Store) ListCaseActionAttempts(ctx context.Context, executionIDs []string) ([]quack.CaseActionAttempt, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	if len(executionIDs) == 0 {
		return nil, nil
	}

	var attempts []quack.CaseActionAttempt
	if err := s.db.WithContext(ctx).
		Where("execution_id IN ?", executionIDs).
		Order("execution_id ASC, attempt_number ASC").
		Find(&attempts).Error; err != nil {
		return nil, fmt.Errorf("list case action attempts: %w", err)
	}

	return attempts, nil
}

// GetCaseActionExecution retrieves one guild-scoped action for staff recovery controls.
func (s *Store) GetCaseActionExecution(ctx context.Context, guildID, executionID string) (*quack.CaseActionExecution, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var item quack.CaseActionExecution
	result := s.db.WithContext(ctx).Where("id = ? AND case_id IN (SELECT id FROM cases WHERE guild_id = ?)", executionID, guildID).First(&item)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, result.Error
	}
	return &item, nil
}

// ListCaseEvidence returns immutable snapshots and attachment metadata for one case.
func (s *Store) ListCaseEvidence(ctx context.Context, caseID string) ([]quack.CaseEvidenceSnapshot, []quack.CaseEvidenceAttachment, error) {
	if s == nil || s.db == nil {
		return nil, nil, errors.New("database not connected")
	}
	var snapshots []quack.CaseEvidenceSnapshot
	if err := s.db.WithContext(ctx).Where("case_id = ?", caseID).Order("created_at ASC").Find(&snapshots).Error; err != nil {
		return nil, nil, fmt.Errorf("list case evidence: %w", err)
	}
	ids := make([]string, 0, len(snapshots))
	for _, item := range snapshots {
		ids = append(ids, item.ID)
	}
	var attachments []quack.CaseEvidenceAttachment
	if len(ids) > 0 {
		if err := s.db.WithContext(ctx).Where("evidence_id IN ?", ids).Order("created_at ASC").Find(&attachments).Error; err != nil {
			return nil, nil, fmt.Errorf("list evidence attachments: %w", err)
		}
	}
	return snapshots, attachments, nil
}

// GetCaseNotification returns the one notification record when the selected level enabled member delivery.
func (s *Store) GetCaseNotification(ctx context.Context, caseID string) (*quack.CaseNotification, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	var item quack.CaseNotification
	result := s.db.WithContext(ctx).Where("case_id = ?", caseID).First(&item)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if result.Error != nil {
		return nil, fmt.Errorf("get case notification: %w", result.Error)
	}
	return &item, nil
}

// filteredCasesQuery adds validated case filters to a caller-owned GORM query.
func filteredCasesQuery(query *gorm.DB, params quack.ListCasesParams) *gorm.DB {
	query = query.Where("guild_id = ?", params.GuildID)
	if params.TargetDiscordUserID != "" {
		query = query.Where("target_discord_user_id = ?", params.TargetDiscordUserID)
	}
	if params.ModeratorDiscordUserID != "" {
		query = query.Where("moderator_discord_user_id = ?", params.ModeratorDiscordUserID)
	}
	if params.TemplateID != "" {
		query = query.Where("template_id = ?", params.TemplateID)
	}
	if params.Validity != "" {
		query = query.Where("status = ?", params.Validity)
	}
	if params.CaseNumber != "" {
		query = query.Where("case_number = ?", params.CaseNumber)
	}
	if params.CreatedAfter != "" {
		query = query.Where("created_at >= ?", params.CreatedAfter)
	}
	if params.CreatedBefore != "" {
		query = query.Where("created_at <= ?", params.CreatedBefore)
	}
	if params.ActionResult != "" {
		query = query.Where("EXISTS (SELECT 1 FROM case_action_executions cae WHERE cae.case_id = cases.id AND cae.status = ?)", params.ActionResult)
	}
	if params.AppealStatus != "" {
		query = query.Where("EXISTS (SELECT 1 FROM appeals a WHERE a.case_id = cases.id AND a.status = ?)", params.AppealStatus)
	}
	return query
}
