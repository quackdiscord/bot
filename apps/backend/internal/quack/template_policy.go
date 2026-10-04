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
	CaseDecayDays  int                         `json:"case_decay_days"`
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
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionTemplateExport)
	if err := s.requireWrite(ctx, guildContext, action, templateID); err != nil {
		return nil, err
	}
	template, err := s.Get(ctx, guildContext, templateID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, templateID, AuditResultFailure, err.Error())
		return nil, err
	}
	input := template.EditInput()
	policy := &TemplatePolicy{
		SchemaVersion:  1,
		Slug:           input.Slug,
		Name:           input.Name,
		Description:    input.Description,
		OfficialReason: input.ReasonTemplate,
		CaseDecayDays:  input.CaseDecayDays,
		Appealable:     input.Appealable,
		ContextFields:  input.ContextFields,
		Levels:         input.Levels,
	}
	if err := s.audit(ctx, guildContext, action, templateID, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return policy, nil
}

// Import creates a new template in this guild from an exported policy.
func (s *TemplateService) Import(ctx context.Context, guildContext *GuildStaffContext, input TemplateImportInput) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionTemplateImport)
	if err := s.requireWrite(ctx, guildContext, action, ""); err != nil {
		return nil, err
	}
	var err error
	switch {
	case !input.Confirm:
		err = templateValidationError("template import must be explicitly confirmed")
	case input.Policy.SchemaVersion != 1:
		err = templateValidationError("unsupported template policy schema_version")
	}
	if err != nil {
		_ = s.audit(ctx, guildContext, action, "unknown", AuditResultFailure, err.Error())
		return nil, err
	}
	policy := input.Policy
	return s.create(ctx, guildContext, action, TemplateInput{
		Slug:           policy.Slug,
		Name:           policy.Name,
		Description:    policy.Description,
		ReasonTemplate: policy.OfficialReason,
		CaseDecayDays:  policy.CaseDecayDays,
		Appealable:     policy.Appealable,
		ContextFields:  policy.ContextFields,
		Levels:         policy.Levels,
	}, "Template imported")
}

// EditInput turns the template back into the input that would recreate it,
// with ExpectedVersion set, as the starting point for an edit. The slices are
// copies, so changing the input never changes the response.
func (t TemplateResponse) EditInput() TemplateInput {
	input := TemplateInput{
		Slug:            t.Slug,
		Name:            t.Name,
		Description:     t.Description,
		ReasonTemplate:  t.ReasonTemplate,
		CaseDecayDays:   t.CaseDecayDays,
		Appealable:      t.Appealable,
		ExpectedVersion: t.Version,
	}
	for _, field := range t.ContextFields {
		input.ContextFields = append(input.ContextFields, TemplateContextFieldInput{
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Position:  field.Position,
			Required:  field.Required,
		})
	}
	for _, level := range t.Levels {
		levelInput := TemplateLevelInput{
			Name:             level.Name,
			Position:         level.Position,
			IsDefault:        level.IsDefault,
			TriggerCaseCount: level.TriggerCaseCount,
			NotifyUser:       level.NotifyUser,
		}
		for _, action := range level.Actions {
			levelInput.Actions = append(levelInput.Actions, TemplateActionInput{
				ActionType:             action.ActionType,
				TimeoutDurationSeconds: action.TimeoutDurationSeconds,
				DeleteMessageSeconds:   action.DeleteMessageSeconds,
				MaxRetries:             int(action.MaxRetries),
			})
		}
		input.Levels = append(input.Levels, levelInput)
	}
	return input
}
