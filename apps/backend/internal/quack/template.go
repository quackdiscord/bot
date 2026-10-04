package quack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// TemplateService manages a guild's case templates. Reads need case or
// template read access; writes need Manage Guild. Writes and denials are
// audited; successful reads are not.
type TemplateService struct {
	store TemplateStore
}

// NewTemplateService returns a TemplateService backed by store.
func NewTemplateService(store TemplateStore) *TemplateService {
	return &TemplateService{store: store}
}

// TemplateInput is a template as an admin submits it, before validation.
type TemplateInput struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ReasonTemplate string `json:"reason_template"`
	// CaseDecayDays limits escalation counting to recent cases, from 0 to
	// MaxCaseDecayDays. Zero counts all-time history.
	CaseDecayDays int                         `json:"case_decay_days"`
	Appealable    bool                        `json:"appealable"`
	ContextFields []TemplateContextFieldInput `json:"context_fields"`
	Levels        []TemplateLevelInput        `json:"levels"`
	// ExpectedVersion, when set on an update, is the version the editor
	// started from. The update fails with ErrTemplateConflict if the
	// template has changed since.
	ExpectedVersion uint `json:"expected_version,omitempty"`
}

// TemplateContextFieldInput is a context field as submitted.
type TemplateContextFieldInput struct {
	Key       string           `json:"key"`
	Label     string           `json:"label"`
	FieldType ContextFieldType `json:"type"`
	Position  int              `json:"position"`
	Required  bool             `json:"required"`
}

// TemplateLevelInput is an escalation level as submitted. Exactly one level
// is the default; the others need a distinct positive TriggerCaseCount.
type TemplateLevelInput struct {
	Name             string                `json:"name"`
	Position         int                   `json:"position"`
	IsDefault        bool                  `json:"is_default"`
	TriggerCaseCount int                   `json:"trigger_case_count"`
	NotifyUser       bool                  `json:"notify_user"`
	Actions          []TemplateActionInput `json:"actions"`
}

// TemplateActionInput is a level's timeout, kick, or ban. Only the setting
// that belongs to the action type may be set.
type TemplateActionInput struct {
	ActionType             ActionType `json:"action_type"`
	TimeoutDurationSeconds int        `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int        `json:"delete_message_seconds,omitempty"`
	MaxRetries             int        `json:"max_retries"`
}

// TemplateResponse is the current version of a template.
type TemplateResponse struct {
	ID                     string                         `json:"id"`
	GuildID                string                         `json:"guild_id"`
	Slug                   string                         `json:"slug"`
	Name                   string                         `json:"name"`
	Description            string                         `json:"description"`
	ReasonTemplate         string                         `json:"reason_template"`
	CaseDecayDays          int                            `json:"case_decay_days"`
	Appealable             bool                           `json:"appealable"`
	Version                uint                           `json:"version"`
	CreatedByDiscordUserID string                         `json:"created_by_discord_user_id"`
	UpdatedByDiscordUserID string                         `json:"updated_by_discord_user_id"`
	ArchivedAt             *time.Time                     `json:"archived_at"`
	ContextFields          []TemplateContextFieldResponse `json:"context_fields"`
	Levels                 []TemplateLevelResponse        `json:"levels"`
}

// TemplateContextFieldResponse is a stored context field.
type TemplateContextFieldResponse struct {
	ID        string           `json:"id"`
	Key       string           `json:"key"`
	Label     string           `json:"label"`
	FieldType ContextFieldType `json:"type"`
	Position  int              `json:"position"`
	Required  bool             `json:"required"`
}

// TemplateLevelDetails is a stored level without its action.
type TemplateLevelDetails struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Position         int    `json:"position"`
	IsDefault        bool   `json:"is_default"`
	TriggerCaseCount int    `json:"trigger_case_count"`
	NotifyUser       bool   `json:"notify_user"`
}

// TemplateLevelResponse is a stored level with its action.
type TemplateLevelResponse struct {
	TemplateLevelDetails
	Actions []TemplateActionResponse `json:"actions"`
}

// TemplateActionResponse is a stored level action with its settings
// unpacked. Case snapshots keep the selected level's action in this shape.
type TemplateActionResponse struct {
	ID                     string     `json:"id"`
	ActionType             ActionType `json:"action_type"`
	TimeoutDurationSeconds int        `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int        `json:"delete_message_seconds,omitempty"`
	MaxRetries             uint8      `json:"max_retries"`
}

// List returns all of the guild's templates, including archived ones. Only
// denials are audited.
func (s *TemplateService) List(ctx context.Context, guildContext *GuildStaffContext) ([]TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	if err := s.requireRead(ctx, guildContext, "list"); err != nil {
		return nil, err
	}
	templates, err := s.store.ListCaseTemplates(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	out := make([]TemplateResponse, 0, len(templates))
	for _, template := range templates {
		out = append(out, templateResponse(template))
	}
	return out, nil
}

// ListActive returns the templates that can be applied to new cases.
func (s *TemplateService) ListActive(ctx context.Context, guildContext *GuildStaffContext) ([]TemplateResponse, error) {
	all, err := s.List(ctx, guildContext)
	if err != nil {
		return nil, err
	}
	active := make([]TemplateResponse, 0, len(all))
	for _, template := range all {
		if template.ArchivedAt == nil {
			active = append(active, template)
		}
	}
	return active, nil
}

// Get returns one template. Only denials are audited.
func (s *TemplateService) Get(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	if err := s.requireRead(ctx, guildContext, templateID); err != nil {
		return nil, err
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, ErrTemplateNotFound
	}
	response := templateResponse(*template)
	return &response, nil
}

// Create validates input and stores it as a new template at version 1.
func (s *TemplateService) Create(ctx context.Context, guildContext *GuildStaffContext, input TemplateInput) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionTemplateCreate)
	if err := s.requireWrite(ctx, guildContext, action, ""); err != nil {
		return nil, err
	}
	return s.create(ctx, guildContext, action, input, "Template created")
}

// Update replaces a template's policy and bumps its version. Existing cases
// keep the snapshot they were created with. A concurrent edit makes it fail
// with ErrTemplateConflict rather than silently overwrite the other edit.
func (s *TemplateService) Update(ctx context.Context, guildContext *GuildStaffContext, templateID string, input TemplateInput) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionTemplateUpdate)
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}
	existing, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrTemplateNotFound
	}
	if input.ExpectedVersion != 0 && input.ExpectedVersion != existing.Template.Version {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, ErrTemplateConflict.Error())
		return nil, ErrTemplateConflict
	}
	normalized, err := s.validate(ctx, guildContext, templateID, input)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, err.Error())
		return nil, err
	}
	updated, err := s.store.UpdateCaseTemplate(ctx, UpdateCaseTemplateParams{
		GuildID:         guildContext.Guild.ID,
		TemplateID:      templateID,
		ExpectedVersion: existing.Template.Version,
		Template:        normalized.Template,
		ContextFields:   normalized.ContextFields,
		Levels:          normalized.Levels,
		Audit:           staffAudit(ctx, guildContext, action, "case_template", templateID, AuditResultSuccess, ""),
	})
	if err != nil {
		if errors.Is(err, ErrTemplateConflict) {
			_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, err.Error())
		}
		return nil, err
	}
	if updated == nil {
		return nil, ErrTemplateNotFound
	}
	return logTemplate(ctx, "Template updated", updated), nil
}

// Archive hides a template from new cases. Its history stays intact and it
// can be restored.
func (s *TemplateService) Archive(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	return s.setArchived(ctx, guildContext, strings.TrimSpace(templateID), true)
}

// Restore makes an archived template available again. Its identity and
// version are unchanged.
func (s *TemplateService) Restore(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplateResponse, error) {
	return s.setArchived(ctx, guildContext, strings.TrimSpace(templateID), false)
}

// create validates input and stores it as a new template at version 1,
// auditing any failure under action. Create and Import share it.
func (s *TemplateService) create(ctx context.Context, guildContext *GuildStaffContext, action string, input TemplateInput, logMessage string) (*TemplateResponse, error) {
	normalized, err := s.validate(ctx, guildContext, "", input)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "unknown", AuditResultFailure, err.Error())
		return nil, err
	}
	created, err := s.store.CreateCaseTemplate(ctx, CreateCaseTemplateParams{
		Template:      normalized.Template,
		ContextFields: normalized.ContextFields,
		Levels:        normalized.Levels,
		Audit:         staffAudit(ctx, guildContext, action, "case_template", "", AuditResultSuccess, ""),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "unknown", AuditResultFailure, err.Error())
		return nil, err
	}
	return logTemplate(ctx, logMessage, created), nil
}

// setArchived archives or restores a template.
func (s *TemplateService) setArchived(ctx context.Context, guildContext *GuildStaffContext, templateID string, archive bool) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	action, logMessage := string(AuditActionTemplateRestore), "Template restored"
	change := s.store.RestoreCaseTemplate
	if archive {
		action, logMessage = string(AuditActionTemplateArchive), "Template archived"
		change = s.store.ArchiveCaseTemplate
	}
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}
	audit := staffAudit(ctx, guildContext, action, "case_template", templateID, AuditResultSuccess, "")
	changed, err := change(ctx, guildContext.Guild.ID, templateID, audit)
	if err == nil && changed == nil {
		err = ErrTemplateNotFound
	}
	if err != nil {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, err.Error())
		return nil, err
	}
	return logTemplate(ctx, logMessage, changed), nil
}

// requireRead checks template read access, auditing a denial.
func (s *TemplateService) requireRead(ctx context.Context, guildContext *GuildStaffContext, templateID string) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return errNoGuildContext
	}
	if !guildContext.Can(PermissionActionCaseTemplateRead) {
		_ = s.audit(ctx, guildContext, string(AuditActionTemplateRead), templateID, AuditResultDenied, ErrTemplatePermissionDenied.Error())
		return ErrTemplatePermissionDenied
	}
	return nil
}

// requireWrite checks Manage Guild before any template write, auditing the
// denial without reading the template.
func (s *TemplateService) requireWrite(ctx context.Context, guildContext *GuildStaffContext, action, templateID string) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil ||
		!guildContext.Can(PermissionActionCaseTemplateWrite) {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultDenied, "permission_denied")
		return ErrTemplatePermissionDenied
	}
	return nil
}

func (s *TemplateService) audit(ctx context.Context, guildContext *GuildStaffContext, action, templateID string, result AuditResult, failureReason string) error {
	return recordStaffAudit(ctx, s.store, guildContext, action, "case_template", templateID, result, failureReason)
}

// logTemplate logs a template change and returns the template's response.
func logTemplate(ctx context.Context, message string, template *ExpandedCaseTemplate) *TemplateResponse {
	slog.InfoContext(ctx, message, "guild_id", template.Template.GuildID,
		"template_id", template.Template.ID, "version", template.Template.Version)
	response := templateResponse(*template)
	return &response
}

func templateResponse(expanded ExpandedCaseTemplate) TemplateResponse {
	template := expanded.Template
	response := TemplateResponse{
		ID:                     template.ID,
		GuildID:                template.GuildID,
		Slug:                   template.Slug,
		Name:                   template.Name,
		Description:            template.Description,
		ReasonTemplate:         template.ReasonTemplate,
		CaseDecayDays:          template.CaseDecayDays,
		Appealable:             template.Appealable,
		Version:                template.Version,
		CreatedByDiscordUserID: template.CreatedByDiscordUserID,
		UpdatedByDiscordUserID: template.UpdatedByDiscordUserID,
		ArchivedAt:             template.ArchivedAt,
		ContextFields:          contextFieldResponses(expanded.ContextFields),
		Levels:                 make([]TemplateLevelResponse, 0, len(expanded.Levels)),
	}
	for _, level := range expanded.Levels {
		response.Levels = append(response.Levels, TemplateLevelResponse{
			TemplateLevelDetails: templateLevelDetails(level.Level),
			Actions:              templateActionResponses(level.Actions),
		})
	}
	return response
}

func contextFieldResponses(fields []CaseTemplateContextField) []TemplateContextFieldResponse {
	out := make([]TemplateContextFieldResponse, 0, len(fields))
	for _, field := range fields {
		out = append(out, TemplateContextFieldResponse{
			ID:        field.ID,
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Position:  field.Position,
			Required:  field.Required,
		})
	}
	return out
}

func templateLevelDetails(level CaseTemplateLevel) TemplateLevelDetails {
	return TemplateLevelDetails{
		ID:               level.ID,
		Name:             level.Name,
		Position:         level.Position,
		IsDefault:        level.IsDefault,
		TriggerCaseCount: level.TriggerCaseCount,
		NotifyUser:       level.NotifyUser,
	}
}

func templateActionResponses(actions []CaseTemplateLevelAction) []TemplateActionResponse {
	out := make([]TemplateActionResponse, 0, len(actions))
	for _, action := range actions {
		config := decodeActionConfig(action.ConfigJSON)
		out = append(out, TemplateActionResponse{
			ID:                     action.ID,
			ActionType:             action.ActionType,
			TimeoutDurationSeconds: config.DurationSeconds,
			DeleteMessageSeconds:   config.DeleteMessageSeconds,
			MaxRetries:             action.MaxRetries,
		})
	}
	return out
}
