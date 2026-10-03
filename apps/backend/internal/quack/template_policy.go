package quack

import "context"

// TemplatePolicy is a template stripped of anything guild-specific, for
// sharing between guilds.
type TemplatePolicy struct {
	SchemaVersion  int                         `json:"schema_version"`
	Slug           string                      `json:"slug"`
	Name           string                      `json:"name"`
	Description    string                      `json:"description"`
	OfficialReason string                      `json:"official_reason"`
	Appealable     bool                        `json:"appealable"`
	ContextFields  []TemplateContextFieldInput `json:"context_fields"`
	Levels         []TemplateLevelInput        `json:"levels"`
}

// TemplateImportInput is an exported policy to import. Confirm must be set,
// since an imported template is live as soon as it is created.
type TemplateImportInput struct {
	Confirm bool           `json:"confirm"`
	Policy  TemplatePolicy `json:"policy"`
}

// Export returns a template's policy for import elsewhere. Guild identity,
// history, and authorship are left out.
func (s *TemplateService) Export(ctx context.Context, guildContext *GuildStaffContext, templateID string) (*TemplatePolicy, error) {
	const action = string(AuditActionTemplateExport)
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}
	template, err := s.Get(ctx, guildContext, templateID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, err.Error())
		return nil, err
	}
	policy := &TemplatePolicy{
		SchemaVersion:  1,
		Slug:           template.Slug,
		Name:           template.Name,
		Description:    template.Description,
		OfficialReason: template.ReasonTemplate,
		Appealable:     template.Appealable,
	}
	for _, f := range template.ContextFields {
		policy.ContextFields = append(policy.ContextFields, TemplateContextFieldInput{
			Key: f.Key, Label: f.Label, FieldType: f.FieldType, Position: f.Position, Required: f.Required,
		})
	}
	for _, level := range template.Levels {
		in := TemplateLevelInput{
			Name:             level.Name,
			Position:         level.Position,
			IsDefault:        level.IsDefault,
			TriggerCaseCount: level.TriggerCaseCount,
			NotifyUser:       level.NotifyUser,
		}
		for _, a := range level.Actions {
			in.Actions = append(in.Actions, TemplateActionInput{
				ActionType:             a.ActionType,
				TimeoutDurationSeconds: a.TimeoutDurationSeconds,
				DeleteMessageSeconds:   a.DeleteMessageSeconds,
				MaxRetries:             int(a.MaxRetries),
			})
		}
		policy.Levels = append(policy.Levels, in)
	}
	if err := s.audit(ctx, guildContext, action, templateID, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return policy, nil
}

// Import creates a new template in this guild from an exported policy.
func (s *TemplateService) Import(ctx context.Context, guildContext *GuildStaffContext, input TemplateImportInput) (*TemplateResponse, error) {
	const action = string(AuditActionTemplateImport)
	if err := s.requireWrite(ctx, guildContext, action, ""); err != nil {
		return nil, err
	}
	fail := func(err error) (*TemplateResponse, error) {
		_ = s.audit(ctx, guildContext, action, "unknown", AuditResultFailure, err.Error())
		return nil, err
	}
	if !input.Confirm {
		return fail(templateValidationError("template import must be explicitly confirmed"))
	}
	if input.Policy.SchemaVersion != 1 {
		return fail(templateValidationError("unsupported template policy schema_version"))
	}
	normalized, err := s.validate(ctx, guildContext, "", TemplateInput{
		Slug:           input.Policy.Slug,
		Name:           input.Policy.Name,
		Description:    input.Policy.Description,
		ReasonTemplate: input.Policy.OfficialReason,
		Appealable:     input.Policy.Appealable,
		ContextFields:  input.Policy.ContextFields,
		Levels:         input.Policy.Levels,
	})
	if err != nil {
		return fail(err)
	}
	expanded, err := s.store.CreateCaseTemplate(ctx, CreateCaseTemplateParams{
		Template:      normalized.Template,
		ContextFields: normalized.ContextFields,
		Levels:        normalized.Levels,
		Audit:         staffAudit(ctx, guildContext, action, "case_template", "", AuditResultSuccess, ""),
	})
	if err != nil {
		return fail(err)
	}
	logTemplate(ctx, "Template imported", expanded)
	response := templateResponse(*expanded)
	return &response, nil
}
