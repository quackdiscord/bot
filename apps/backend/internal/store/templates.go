package store

import (
	"context"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// CreateCaseTemplate saves a new template at version 1 with its fields,
// levels, and actions.
func (s *Store) CreateCaseTemplate(ctx context.Context, params quack.CreateCaseTemplateParams) (*quack.ExpandedCaseTemplate, error) {
	now := time.Now().UTC()
	var id string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		id, err = createTemplate(tx, params.Template, params.ContextFields, params.Levels, now)
		if err != nil {
			return err
		}
		return writeAudit(tx, params.Audit, id, now)
	})
	if err != nil {
		return nil, err
	}
	return s.GetCaseTemplateExpanded(ctx, params.Template.GuildID, id)
}

// ListCaseTemplates returns every template in a guild, archived ones
// included, ordered by slug.
func (s *Store) ListCaseTemplates(ctx context.Context, guildID string) ([]quack.ExpandedCaseTemplate, error) {
	var records []templateRecord
	db := s.db.WithContext(ctx)
	if err := db.Where("guild_id = ?", guildID).Order("slug ASC").Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list case templates: %w", err)
	}
	return expandTemplates(db, records)
}

// GetCaseTemplateExpanded returns a guild's template with its fields,
// levels, and actions, or nil.
func (s *Store) GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*quack.ExpandedCaseTemplate, error) {
	return expandTemplate(s.db.WithContext(ctx), guildID, templateID)
}

// GetCaseTemplateBySlug returns a guild's template by slug, or nil.
func (s *Store) GetCaseTemplateBySlug(ctx context.Context, guildID, slug string) (*quack.CaseTemplate, error) {
	var record templateRecord
	found, err := first(s.db.WithContext(ctx).Where("guild_id = ? AND slug = ?", guildID, slug), &record)
	if err != nil || !found {
		return nil, wrap("get case template by slug", err)
	}
	template := record.model()
	return &template, nil
}

// UpdateCaseTemplate replaces a template's policy, including all of its
// fields, levels, and actions, and bumps its version. Cases keep the snapshot
// of the version they were created under. It returns nil when the template is
// not in the guild.
func (s *Store) UpdateCaseTemplate(ctx context.Context, params quack.UpdateCaseTemplateParams) (*quack.ExpandedCaseTemplate, error) {
	err := s.changeTemplate(ctx, params.GuildID, params.TemplateID, params.Audit, func(tx *gorm.DB, r *templateRecord, now time.Time) error {
		r.Slug = params.Template.Slug
		r.Name = params.Template.Name
		r.Description = params.Template.Description
		r.ReasonTemplate = params.Template.ReasonTemplate
		r.Appealable = params.Template.Appealable
		r.UpdatedByDiscordUserID = params.Template.UpdatedByDiscordUserID
		r.Version++
		levelIDs := tx.Model(&levelRecord{}).Select("id").Where("template_id = ?", r.ID)
		if err := tx.Where("level_id IN (?)", levelIDs).Delete(&levelActionRecord{}).Error; err != nil {
			return fmt.Errorf("delete level actions: %w", err)
		}
		if err := tx.Where("template_id = ?", r.ID).Delete(&levelRecord{}).Error; err != nil {
			return fmt.Errorf("delete levels: %w", err)
		}
		if err := tx.Where("template_id = ?", r.ID).Delete(&contextFieldRecord{}).Error; err != nil {
			return fmt.Errorf("delete context fields: %w", err)
		}
		return createTemplateChildren(tx, r.ID, params.ContextFields, params.Levels, now)
	})
	if err != nil {
		return nil, notFoundIsNil(err)
	}
	return s.GetCaseTemplateExpanded(ctx, params.GuildID, params.TemplateID)
}

// ArchiveCaseTemplate hides a template from new cases. Its cases and history
// are untouched. It returns nil when the template is not in the guild.
func (s *Store) ArchiveCaseTemplate(ctx context.Context, guildID, templateID string, audit *quack.AuditLogEntry) (*quack.ExpandedCaseTemplate, error) {
	err := s.changeTemplate(ctx, guildID, templateID, audit, func(_ *gorm.DB, r *templateRecord, now time.Time) error {
		r.ArchivedAt = &now
		return nil
	})
	if err != nil {
		return nil, notFoundIsNil(err)
	}
	return s.GetCaseTemplateExpanded(ctx, guildID, templateID)
}

// RestoreCaseTemplate makes an archived template usable again without
// changing its version. It returns nil when the template is not in the guild.
func (s *Store) RestoreCaseTemplate(ctx context.Context, guildID, templateID string, audit *quack.AuditLogEntry) (*quack.ExpandedCaseTemplate, error) {
	err := s.changeTemplate(ctx, guildID, templateID, audit, func(_ *gorm.DB, r *templateRecord, _ time.Time) error {
		r.ArchivedAt = nil
		return nil
	})
	if err != nil {
		return nil, notFoundIsNil(err)
	}
	return s.GetCaseTemplateExpanded(ctx, guildID, templateID)
}

// changeTemplate locks a guild's template, applies change, saves it, and
// audits it, all in one transaction. A missing template is errNotFound.
func (s *Store) changeTemplate(ctx context.Context, guildID, templateID string, audit *quack.AuditLogEntry,
	change func(*gorm.DB, *templateRecord, time.Time) error) error {
	now := time.Now().UTC()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record templateRecord
		found, err := first(forUpdate(tx).Where("id = ? AND guild_id = ?", templateID, guildID), &record)
		if err != nil {
			return fmt.Errorf("get case template: %w", err)
		}
		if !found {
			return errNotFound
		}
		if err := change(tx, &record, now); err != nil {
			return err
		}
		record.UpdatedAt = now
		if err := tx.Save(&record).Error; err != nil {
			return fmt.Errorf("save case template: %w", err)
		}
		return writeAudit(tx, audit, record.ID, now)
	})
}

// createTemplate inserts a version 1 template and its children and returns
// its ID.
func createTemplate(tx *gorm.DB, template quack.CaseTemplate, fields []quack.CaseTemplateContextField,
	levels []quack.ExpandedCaseTemplateLevel, now time.Time) (string, error) {
	stamp(&template.ULIDModel, now)
	template.Version = 1
	record := newTemplateRecord(template)
	if err := tx.Create(&record).Error; err != nil {
		return "", fmt.Errorf("create case template: %w", err)
	}
	return record.ID, createTemplateChildren(tx, record.ID, fields, levels, now)
}

// createTemplateChildren inserts a template's fields, levels, and actions.
func createTemplateChildren(tx *gorm.DB, templateID string, fields []quack.CaseTemplateContextField,
	levels []quack.ExpandedCaseTemplateLevel, now time.Time) error {
	fieldRecords := make([]contextFieldRecord, 0, len(fields))
	for _, field := range fields {
		field.TemplateID = templateID
		stamp(&field.ULIDModel, now)
		fieldRecords = append(fieldRecords, newContextFieldRecord(field))
	}
	var levelRecords []levelRecord
	var actionRecords []levelActionRecord
	for _, expanded := range levels {
		level := expanded.Level
		level.TemplateID = templateID
		stamp(&level.ULIDModel, now)
		levelRecords = append(levelRecords, newLevelRecord(level))
		for _, action := range expanded.Actions {
			action.LevelID = level.ID
			stamp(&action.ULIDModel, now)
			actionRecords = append(actionRecords, newLevelActionRecord(action))
		}
	}
	if err := createAll(tx, fieldRecords); err != nil {
		return fmt.Errorf("create context fields: %w", err)
	}
	if err := createAll(tx, levelRecords); err != nil {
		return fmt.Errorf("create levels: %w", err)
	}
	if err := createAll(tx, actionRecords); err != nil {
		return fmt.Errorf("create level actions: %w", err)
	}
	return nil
}

// createAll inserts records in one statement. GORM rejects an empty batch, so
// an empty slice is a no-op.
func createAll[T any](tx *gorm.DB, records []T) error {
	if len(records) == 0 {
		return nil
	}
	return tx.Create(&records).Error
}

// expandTemplate loads one guild template with its children, or nil.
func expandTemplate(db *gorm.DB, guildID, templateID string) (*quack.ExpandedCaseTemplate, error) {
	var record templateRecord
	found, err := first(db.Where("id = ? AND guild_id = ?", templateID, guildID), &record)
	if err != nil || !found {
		return nil, wrap("get case template", err)
	}
	expanded, err := expandTemplates(db, []templateRecord{record})
	if err != nil {
		return nil, err
	}
	return &expanded[0], nil
}

// expandTemplates loads the children of many templates in three queries,
// however many templates and levels there are.
func expandTemplates(db *gorm.DB, templates []templateRecord) ([]quack.ExpandedCaseTemplate, error) {
	if len(templates) == 0 {
		return []quack.ExpandedCaseTemplate{}, nil
	}
	ids := make([]string, len(templates))
	for i, t := range templates {
		ids[i] = t.ID
	}
	var fields []contextFieldRecord
	if err := db.Where("template_id IN ?", ids).Order("position ASC").Find(&fields).Error; err != nil {
		return nil, fmt.Errorf("list context fields: %w", err)
	}
	var levels []levelRecord
	if err := db.Where("template_id IN ?", ids).Order("position ASC").Find(&levels).Error; err != nil {
		return nil, fmt.Errorf("list levels: %w", err)
	}
	levelIDs := make([]string, len(levels))
	for i, level := range levels {
		levelIDs[i] = level.ID
	}
	var actions []levelActionRecord
	if len(levelIDs) > 0 {
		if err := db.Where("level_id IN ?", levelIDs).Order("created_at ASC, id ASC").Find(&actions).Error; err != nil {
			return nil, fmt.Errorf("list level actions: %w", err)
		}
	}

	actionsByLevel := make(map[string][]quack.CaseTemplateLevelAction)
	for _, action := range actions {
		actionsByLevel[action.LevelID] = append(actionsByLevel[action.LevelID], action.model())
	}
	fieldsByTemplate := make(map[string][]quack.CaseTemplateContextField)
	for _, field := range fields {
		fieldsByTemplate[field.TemplateID] = append(fieldsByTemplate[field.TemplateID], field.model())
	}
	levelsByTemplate := make(map[string][]quack.ExpandedCaseTemplateLevel)
	for _, level := range levels {
		levelActions := actionsByLevel[level.ID]
		if levelActions == nil {
			levelActions = []quack.CaseTemplateLevelAction{}
		}
		levelsByTemplate[level.TemplateID] = append(levelsByTemplate[level.TemplateID],
			quack.ExpandedCaseTemplateLevel{Level: level.model(), Actions: levelActions})
	}

	expanded := make([]quack.ExpandedCaseTemplate, len(templates))
	for i, t := range templates {
		expanded[i] = quack.ExpandedCaseTemplate{
			Template:      t.model(),
			ContextFields: fieldsByTemplate[t.ID],
			Levels:        levelsByTemplate[t.ID],
		}
		if expanded[i].ContextFields == nil {
			expanded[i].ContextFields = []quack.CaseTemplateContextField{}
		}
		if expanded[i].Levels == nil {
			expanded[i].Levels = []quack.ExpandedCaseTemplateLevel{}
		}
	}
	return expanded, nil
}

func newTemplateRecord(t quack.CaseTemplate) templateRecord {
	return templateRecord{
		ID:                     t.ID,
		CreatedAt:              t.CreatedAt,
		UpdatedAt:              t.UpdatedAt,
		GuildID:                t.GuildID,
		Slug:                   t.Slug,
		Name:                   t.Name,
		Description:            t.Description,
		ReasonTemplate:         t.ReasonTemplate,
		Appealable:             t.Appealable,
		Version:                t.Version,
		CreatedByDiscordUserID: t.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: t.UpdatedByDiscordUserID,
		ArchivedAt:             t.ArchivedAt,
	}
}

func (r templateRecord) model() quack.CaseTemplate {
	return quack.CaseTemplate{
		ULIDModel:              ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		GuildID:                r.GuildID,
		Slug:                   r.Slug,
		Name:                   r.Name,
		Description:            r.Description,
		ReasonTemplate:         r.ReasonTemplate,
		Appealable:             r.Appealable,
		Version:                r.Version,
		CreatedByDiscordUserID: r.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: r.UpdatedByDiscordUserID,
		ArchivedAt:             r.ArchivedAt,
	}
}

func newContextFieldRecord(f quack.CaseTemplateContextField) contextFieldRecord {
	return contextFieldRecord{
		ID:         f.ID,
		CreatedAt:  f.CreatedAt,
		UpdatedAt:  f.UpdatedAt,
		TemplateID: f.TemplateID,
		Key:        f.Key,
		Label:      f.Label,
		FieldType:  f.FieldType,
		Position:   f.Position,
		Required:   f.Required,
	}
}

func (r contextFieldRecord) model() quack.CaseTemplateContextField {
	return quack.CaseTemplateContextField{
		ULIDModel:  ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		TemplateID: r.TemplateID,
		Key:        r.Key,
		Label:      r.Label,
		FieldType:  r.FieldType,
		Position:   r.Position,
		Required:   r.Required,
	}
}

func newLevelRecord(l quack.CaseTemplateLevel) levelRecord {
	return levelRecord{
		ID:               l.ID,
		CreatedAt:        l.CreatedAt,
		UpdatedAt:        l.UpdatedAt,
		TemplateID:       l.TemplateID,
		Position:         l.Position,
		Name:             l.Name,
		IsDefault:        l.IsDefault,
		TriggerCaseCount: l.TriggerCaseCount,
		NotifyUser:       l.NotifyUser,
	}
}

func (r levelRecord) model() quack.CaseTemplateLevel {
	return quack.CaseTemplateLevel{
		ULIDModel:        ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		TemplateID:       r.TemplateID,
		Position:         r.Position,
		Name:             r.Name,
		IsDefault:        r.IsDefault,
		TriggerCaseCount: r.TriggerCaseCount,
		NotifyUser:       r.NotifyUser,
	}
}

func newLevelActionRecord(a quack.CaseTemplateLevelAction) levelActionRecord {
	return levelActionRecord{
		ID:         a.ID,
		CreatedAt:  a.CreatedAt,
		UpdatedAt:  a.UpdatedAt,
		LevelID:    a.LevelID,
		ActionType: a.ActionType,
		ConfigJSON: a.ConfigJSON,
		MaxRetries: a.MaxRetries,
	}
}

func (r levelActionRecord) model() quack.CaseTemplateLevelAction {
	return quack.CaseTemplateLevelAction{
		ULIDModel:  ulid(r.ID, r.CreatedAt, r.UpdatedAt),
		LevelID:    r.LevelID,
		ActionType: r.ActionType,
		ConfigJSON: r.ConfigJSON,
		MaxRetries: r.MaxRetries,
	}
}
