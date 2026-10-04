package quack

import (
	"context"
	"slices"
	"strings"
)

// honeypotTemplateSlug is the slug of the template honeypot setup creates.
const honeypotTemplateSlug = "honeypot"

// EnsureHoneypotTemplate returns the guild's honeypot template, creating the
// default one (an appealable ban that notifies the member) through the normal
// validated and audited create path if the guild has none. An existing
// template is returned as the admin left it; an archived one must be restored
// explicitly rather than silently revived.
func (s *TemplateService) EnsureHoneypotTemplate(ctx context.Context, guildContext *GuildStaffContext) (*TemplateResponse, error) {
	ctx = ensureTraceContext(ctx)
	if err := s.requireWrite(ctx, guildContext, string(AuditActionTemplateCreate), ""); err != nil {
		return nil, err
	}
	existing, err := s.store.GetCaseTemplateBySlug(ctx, guildContext.Guild.ID, honeypotTemplateSlug)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.ArchivedAt != nil {
			return nil, templateValidationError("restore the archived honeypot template before setup")
		}
		return s.Get(ctx, guildContext, existing.ID)
	}
	return s.Create(ctx, guildContext, TemplateInput{
		Slug:           honeypotTemplateSlug,
		Name:           "Honeypot",
		Description:    "Applied when a member posts in the honeypot channel.",
		ReasonTemplate: "Posted in the honeypot channel despite the warning.",
		Appealable:     true,
		Levels: []TemplateLevelInput{{
			Name:       "Default",
			Position:   1,
			IsDefault:  true,
			NotifyUser: true,
			Actions:    []TemplateActionInput{{ActionType: ActionBanUser}},
		}},
	})
}

// UnattendedTemplateActions returns the distinct outcomes a template's levels
// can produce, in level order, so an automated workflow can warn members
// about every one of them. An empty ActionType stands for a level that only
// records the case. It is a system read for workflows already authorized
// for the guild: it checks no staff permission and writes no audit entry.
func (s *TemplateService) UnattendedTemplateActions(ctx context.Context, guildID, templateID string) ([]ActionType, error) {
	template, err := s.store.GetCaseTemplateExpanded(ctx, strings.TrimSpace(guildID), strings.TrimSpace(templateID))
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil || len(template.Levels) == 0 {
		return nil, ErrUnattendedTemplateUnavailable
	}
	var actions []ActionType
	for _, level := range template.Levels {
		if action := level.actionType(); !slices.Contains(actions, action) {
			actions = append(actions, action)
		}
	}
	return actions, nil
}
