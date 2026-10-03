package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CreateCaseTemplate creates case template while preserving validation, authorization, and persistence invariants.
func (s *Store) CreateCaseTemplate(ctx context.Context, params quack.CreateCaseTemplateParams) (*quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	template := params.Template
	if err := prepareULIDModel(&template.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare case template model: %w", err)
	}
	template.Version = 1
	templateRecord := caseTemplateRecordFromModel(template)

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Select("*").Create(&templateRecord).Error; err != nil {
			return fmt.Errorf("create case template: %w", err)
		}

		if err := createTemplateLevels(tx, template.ID, params.Levels, now); err != nil {
			return err
		}
		if err := createTemplateContextFields(tx, template.ID, params.ContextFields, now); err != nil {
			return err
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = template.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, template.GuildID, template.ID)
}

// ListCaseTemplates returns case templates subject to authorization, ordering, and filtering constraints.
func (s *Store) ListCaseTemplates(ctx context.Context, guildID string) ([]quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var records []CaseTemplateRecord
	if err := s.db.WithContext(ctx).
		Where("guild_id = ?", guildID).
		Order("slug ASC").
		Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case templates: %w", err)
	}

	expanded := make([]quack.ExpandedCaseTemplate, 0, len(records))
	for _, record := range records {
		template := caseTemplateModelFromRecord(record)
		item, err := s.GetCaseTemplateExpanded(ctx, guildID, template.ID)
		if err != nil {
			// Quarantined legacy policies cannot be projected through the live
			// template contract. Detail reads retain their explicit conflict,
			// while list reads omit them so valid templates remain usable.
			if errors.Is(err, quack.ErrTemplateCompatibilityReviewRequired) {
				continue
			}
			return nil, err
		}
		if item != nil {
			expanded = append(expanded, *item)
		}
	}

	return expanded, nil
}

// GetCaseTemplateExpanded retrieves case template expanded without exposing the underlying adapter implementation.
func (s *Store) GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	return getCaseTemplateExpanded(s.db.WithContext(ctx), guildID, templateID)
}

// GetCaseTemplateBySlug retrieves case template by slug without exposing the underlying adapter implementation.
func (s *Store) GetCaseTemplateBySlug(ctx context.Context, guildID, slug string) (*quack.CaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var record CaseTemplateRecord
	if err := s.db.WithContext(ctx).Where("guild_id = ? AND slug = ?", guildID, slug).First(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template by slug: %w", err)
	}

	template := caseTemplateModelFromRecord(record)
	return &template, nil
}

// UpdateCaseTemplate updates case template while retaining validation, compatibility, and audit requirements.
func (s *Store) UpdateCaseTemplate(ctx context.Context, params quack.UpdateCaseTemplateParams) (*quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var record CaseTemplateRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", params.TemplateID, params.GuildID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for update: %w", err)
		}

		record.Slug = params.Template.Slug
		record.Name = params.Template.Name
		record.Description = params.Template.Description
		record.ReasonTemplate = params.Template.ReasonTemplate
		record.Appealable = params.Template.Appealable
		record.UpdatedByDiscordUserID = params.Template.UpdatedByDiscordUserID
		record.Version++
		record.UpdatedAt = now

		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("update case template: %w", err)
		}

		levelIDs := tx.Model(&CaseTemplateLevelRecord{}).Select("id").Where("template_id = ?", record.ID)
		if err := tx.Where("level_id IN (?)", levelIDs).Delete(&CaseTemplateLevelActionRecord{}).Error; err != nil {
			return fmt.Errorf("replace case template level actions: %w", err)
		}
		if err := tx.Where("template_id = ?", record.ID).Delete(&CaseTemplateLevelRecord{}).Error; err != nil {
			return fmt.Errorf("replace case template levels: %w", err)
		}
		if err := createTemplateLevels(tx, record.ID, params.Levels, now); err != nil {
			return err
		}
		if err := tx.Where("template_id = ?", record.ID).Delete(&CaseTemplateContextFieldRecord{}).Error; err != nil {
			return fmt.Errorf("replace case template context fields: %w", err)
		}
		if err := createTemplateContextFields(tx, record.ID, params.ContextFields, now); err != nil {
			return err
		}

		if params.Audit != nil {
			audit := *params.Audit
			audit.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, params.GuildID, record.ID)
}

// RestoreCaseTemplate makes an archived template available again without changing its identity or version.
func (s *Store) RestoreCaseTemplate(ctx context.Context, guildID, templateID string, audit *quack.AuditLogEntry) (*quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}
	now := time.Now().UTC()
	var record CaseTemplateRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND guild_id = ?", templateID, guildID).First(&record)
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return gorm.ErrRecordNotFound
		}
		if result.Error != nil {
			return fmt.Errorf("get case template for restore: %w", result.Error)
		}
		record.ArchivedAt = nil
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("restore case template: %w", err)
		}
		if audit != nil {
			entry := *audit
			entry.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &entry, now); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.GetCaseTemplateExpanded(ctx, guildID, templateID)
}

// ArchiveCaseTemplate archives case template without deleting historical moderation references.
func (s *Store) ArchiveCaseTemplate(ctx context.Context, guildID, templateID string, audit *quack.AuditLogEntry) (*quack.ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var record CaseTemplateRecord
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", templateID, guildID).First(&record).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for archive: %w", err)
		}

		record.ArchivedAt = &now
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("archive case template: %w", err)
		}

		if audit != nil {
			audit := *audit
			audit.ResourceID = record.ID
			if err := createAuditLogEntry(tx, &audit, now); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, guildID, record.ID)
}
