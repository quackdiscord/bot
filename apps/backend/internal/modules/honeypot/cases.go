package honeypot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// CaseCreator opens a case attributed to Quack itself. *quack.CaseService
// implements it; a honeypot case goes through the same escalation,
// idempotency, and enforcement as any other.
type CaseCreator interface {
	CreateSystemHoneypot(ctx context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error)
}

// caseApplier is the CaseApplier that opens real cases. It re-checks the
// request so nothing but a complete, Quack-attributed honeypot request can
// reach the system case path.
type caseApplier struct{ cases CaseCreator }

// ApplyHoneypotCase checks that the request is a complete, system-attributed
// honeypot request and opens its case.
func (a caseApplier) ApplyHoneypotCase(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	if request.Source != SourceHoneypot || request.ActorType != ActorTypeSystem ||
		strings.TrimSpace(request.ActorDiscordUserID) != "" {
		return ApplyResult{}, errors.New("honeypot case attribution is invalid")
	}
	for _, field := range []string{
		request.GuildID, request.TemplateID, request.TargetDiscordUserID,
		request.ContextChannelDiscordID, request.ContextMessageDiscordID,
		request.ContextURL, request.IdempotencyKey,
	} {
		if strings.TrimSpace(field) == "" {
			return ApplyResult{}, errors.New("honeypot case request is incomplete")
		}
	}
	created, err := a.cases.CreateSystemHoneypot(ctx, request.GuildID, quack.CaseInput{
		TemplateID:              request.TemplateID,
		TargetDiscordUserID:     request.TargetDiscordUserID,
		Source:                  quack.CaseSourceHoneypot,
		ContextChannelDiscordID: request.ContextChannelDiscordID,
		ContextMessageDiscordID: request.ContextMessageDiscordID,
		ContextURL:              request.ContextURL,
		IdempotencyKey:          request.IdempotencyKey,
	})
	if err != nil {
		return ApplyResult{}, err
	}
	return ApplyResult{CaseID: created.ID}, nil
}

// TemplateStore loads a template with its levels and actions.
type TemplateStore interface {
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*quack.ExpandedCaseTemplate, error)
}

// templateValidator is the TemplateValidator over the live template.
type templateValidator struct{ store TemplateStore }

// ValidateHoneypotTemplate accepts only a template that can run with nobody
// at the keyboard: active, no required context fields, exactly one default
// level, and at most one timeout, kick, or ban per level. Member DMs come
// from a level's notify_user, not an action.
func (v templateValidator) ValidateHoneypotTemplate(ctx context.Context, guildID, templateID string) error {
	template, err := v.store.GetCaseTemplateExpanded(ctx, strings.TrimSpace(guildID), strings.TrimSpace(templateID))
	if err != nil {
		return err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return ErrTemplateUnavailable
	}
	for _, field := range template.ContextFields {
		if field.Required {
			return fmt.Errorf("%w: required context field %s cannot be supplied unattended", ErrTemplateUnavailable, field.Key)
		}
	}
	defaults := 0
	for _, level := range template.Levels {
		if level.Level.IsDefault {
			defaults++
		}
		if len(level.Actions) > 1 {
			return fmt.Errorf("%w: template level has multiple actions", ErrTemplateUnavailable)
		}
		for _, action := range level.Actions {
			switch action.ActionType {
			case quack.ActionTimeoutUser, quack.ActionKickUser, quack.ActionBanUser:
			default:
				return fmt.Errorf("%w: unsupported unattended action %s", ErrTemplateUnavailable, action.ActionType)
			}
		}
	}
	if defaults != 1 {
		return fmt.Errorf("%w: template must have exactly one default level", ErrTemplateUnavailable)
	}
	return nil
}
