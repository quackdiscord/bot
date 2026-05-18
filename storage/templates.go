package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/structs"
	"gorm.io/gorm"
)

type ExpandedCaseTemplate struct {
	Template        structs.CaseTemplate
	Actions         []structs.CaseTemplateAction
	EscalationRules []structs.CaseTemplateEscalationRule
}

type CreateCaseTemplateParams struct {
	Template        structs.CaseTemplate
	Actions         []structs.CaseTemplateAction
	EscalationRules []structs.CaseTemplateEscalationRule
	Audit           *structs.AuditLogEntry
}

type UpdateCaseTemplateParams struct {
	GuildID         string
	TemplateID      string
	Template        structs.CaseTemplate
	Actions         []structs.CaseTemplateAction
	EscalationRules []structs.CaseTemplateEscalationRule
	Audit           *structs.AuditLogEntry
}

func (s *Store) CreateCaseTemplate(ctx context.Context, params CreateCaseTemplateParams) (*ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	template := params.Template
	if err := prepareULIDModel(&template.ULIDModel, now); err != nil {
		return nil, fmt.Errorf("prepare case template model: %w", err)
	}
	template.Version = 1

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Select("*").Create(&template).Error; err != nil {
			return fmt.Errorf("create case template: %w", err)
		}
		if !params.Template.Enabled {
			if err := tx.Model(&structs.CaseTemplate{}).Where("id = ?", template.ID).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("set case template enabled state: %w", err)
			}
		}

		if err := createTemplateActions(tx, template.ID, params.Actions, now); err != nil {
			return err
		}
		if err := createTemplateEscalationRules(tx, template.GuildID, template.ID, params.EscalationRules, now); err != nil {
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

func (s *Store) ListCaseTemplates(ctx context.Context, guildID string) ([]ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var templates []structs.CaseTemplate
	if err := s.db.WithContext(ctx).
		Where("guild_id = ? AND archived_at IS NULL", guildID).
		Order("slug ASC").
		Find(&templates).Error; err != nil {
		return nil, fmt.Errorf("list case templates: %w", err)
	}

	expanded := make([]ExpandedCaseTemplate, 0, len(templates))
	for _, template := range templates {
		item, err := s.GetCaseTemplateExpanded(ctx, guildID, template.ID)
		if err != nil {
			return nil, err
		}
		if item != nil {
			expanded = append(expanded, *item)
		}
	}

	return expanded, nil
}

func (s *Store) GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	return getCaseTemplateExpanded(s.db.WithContext(ctx), guildID, templateID)
}

func (s *Store) GetCaseTemplateBySlug(ctx context.Context, guildID, slug string) (*structs.CaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var template structs.CaseTemplate
	if err := s.db.WithContext(ctx).Where("guild_id = ? AND slug = ?", guildID, slug).First(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template by slug: %w", err)
	}

	return &template, nil
}

func (s *Store) UpdateCaseTemplate(ctx context.Context, params UpdateCaseTemplateParams) (*ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var template structs.CaseTemplate
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", params.TemplateID, params.GuildID).First(&template).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for update: %w", err)
		}

		template.Slug = params.Template.Slug
		template.Name = params.Template.Name
		template.Description = params.Template.Description
		template.ReasonTemplate = params.Template.ReasonTemplate
		template.RequiredPermissionBits = params.Template.RequiredPermissionBits
		template.DefaultSeverity = params.Template.DefaultSeverity
		template.DefaultWeight = params.Template.DefaultWeight
		template.DMEnabled = params.Template.DMEnabled
		template.DMTemplate = params.Template.DMTemplate
		template.Enabled = params.Template.Enabled
		template.UpdatedByDiscordUserID = params.Template.UpdatedByDiscordUserID
		template.Version++
		template.UpdatedAt = now

		if err := tx.Save(&template).Error; err != nil {
			return fmt.Errorf("update case template: %w", err)
		}

		if err := tx.Where("template_id = ?", template.ID).Delete(&structs.CaseTemplateAction{}).Error; err != nil {
			return fmt.Errorf("replace case template actions: %w", err)
		}
		if err := tx.Where("template_id = ?", template.ID).Delete(&structs.CaseTemplateEscalationRule{}).Error; err != nil {
			return fmt.Errorf("replace case template escalation rules: %w", err)
		}

		if err := createTemplateActions(tx, template.ID, params.Actions, now); err != nil {
			return err
		}
		if err := createTemplateEscalationRules(tx, template.GuildID, template.ID, params.EscalationRules, now); err != nil {
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
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return s.GetCaseTemplateExpanded(ctx, params.GuildID, template.ID)
}

func (s *Store) ArchiveCaseTemplate(ctx context.Context, guildID, templateID string, audit *structs.AuditLogEntry) (*ExpandedCaseTemplate, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	now := time.Now().UTC()
	var template structs.CaseTemplate
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND guild_id = ?", templateID, guildID).First(&template).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return gorm.ErrRecordNotFound
			}
			return fmt.Errorf("get case template for archive: %w", err)
		}

		template.Enabled = false
		template.ArchivedAt = &now
		template.UpdatedAt = now
		if err := tx.Save(&template).Error; err != nil {
			return fmt.Errorf("archive case template: %w", err)
		}

		if audit != nil {
			audit := *audit
			audit.ResourceID = template.ID
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

	return s.GetCaseTemplateExpanded(ctx, guildID, template.ID)
}

func (s *Store) CreateAuditLogEntry(ctx context.Context, entry *structs.AuditLogEntry) error {
	if s == nil || s.db == nil {
		return errors.New("database not connected")
	}

	return createAuditLogEntry(s.db.WithContext(ctx), entry, time.Now().UTC())
}

func (s *Store) ListAuditLogEntries(ctx context.Context, guildID string) ([]structs.AuditLogEntry, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("database not connected")
	}

	var entries []structs.AuditLogEntry
	if err := s.db.WithContext(ctx).Where("guild_id = ?", guildID).Order("created_at ASC").Find(&entries).Error; err != nil {
		return nil, fmt.Errorf("list audit log entries: %w", err)
	}

	return entries, nil
}

func getCaseTemplateExpanded(db *gorm.DB, guildID, templateID string) (*ExpandedCaseTemplate, error) {
	var template structs.CaseTemplate
	if err := db.Where("id = ? AND guild_id = ?", templateID, guildID).First(&template).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("get case template: %w", err)
	}

	var actions []structs.CaseTemplateAction
	if err := db.Where("template_id = ?", template.ID).Order("position ASC").Find(&actions).Error; err != nil {
		return nil, fmt.Errorf("get case template actions: %w", err)
	}

	var rules []structs.CaseTemplateEscalationRule
	if err := db.Where("template_id = ?", template.ID).Order("priority ASC").Find(&rules).Error; err != nil {
		return nil, fmt.Errorf("get case template escalation rules: %w", err)
	}

	return &ExpandedCaseTemplate{
		Template:        template,
		Actions:         actions,
		EscalationRules: rules,
	}, nil
}

func createTemplateActions(tx *gorm.DB, templateID string, actions []structs.CaseTemplateAction, now time.Time) error {
	for i := range actions {
		action := actions[i]
		action.TemplateID = templateID
		enabled := action.Enabled
		if err := prepareULIDModel(&action.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case template action model: %w", err)
		}
		if err := tx.Select("*").Create(&action).Error; err != nil {
			return fmt.Errorf("create case template action: %w", err)
		}
		if !enabled {
			if err := tx.Model(&structs.CaseTemplateAction{}).Where("id = ?", action.ID).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("set case template action enabled state: %w", err)
			}
		}
	}

	return nil
}

func createTemplateEscalationRules(tx *gorm.DB, guildID, templateID string, rules []structs.CaseTemplateEscalationRule, now time.Time) error {
	for i := range rules {
		rule := rules[i]
		rule.GuildID = guildID
		rule.TemplateID = templateID
		enabled := rule.Enabled
		if err := prepareULIDModel(&rule.ULIDModel, now); err != nil {
			return fmt.Errorf("prepare case template escalation rule model: %w", err)
		}
		if err := tx.Select("*").Create(&rule).Error; err != nil {
			return fmt.Errorf("create case template escalation rule: %w", err)
		}
		if !enabled {
			if err := tx.Model(&structs.CaseTemplateEscalationRule{}).Where("id = ?", rule.ID).Update("enabled", false).Error; err != nil {
				return fmt.Errorf("set case template escalation rule enabled state: %w", err)
			}
		}
	}

	return nil
}

func createAuditLogEntry(db *gorm.DB, entry *structs.AuditLogEntry, now time.Time) error {
	if entry == nil {
		return nil
	}
	if entry.ResourceID == "" {
		entry.ResourceID = "unknown"
	}
	if entry.MetadataJSON == "" {
		entry.MetadataJSON = "{}"
	}
	if err := prepareULIDModel(&entry.ULIDModel, now); err != nil {
		return fmt.Errorf("prepare audit log entry model: %w", err)
	}
	if err := db.Create(entry).Error; err != nil {
		return fmt.Errorf("create audit log entry: %w", err)
	}

	return nil
}
