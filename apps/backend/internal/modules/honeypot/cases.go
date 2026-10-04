package honeypot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// CaseCreator opens a case attributed to Quack itself. *quack.CaseService
// implements it; a honeypot case goes through the same escalation,
// idempotency, and enforcement as any other.
type CaseCreator interface {
	CreateSystemHoneypot(ctx context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error)
}

// CoreStore is the core storage the honeypot reads: templates, to check they
// can run unattended, and cases by idempotency key, so recovery finds a
// case an interrupted attempt already saved. *store.Store implements it.
type CoreStore interface {
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*quack.ExpandedCaseTemplate, error)
	GetCaseByIdempotencyKey(ctx context.Context, guildID, key string) (*quack.Case, error)
}

// caseApplier is the CaseApplier that opens real cases. It re-checks the
// request so nothing but a complete, Quack-attributed honeypot request can
// reach the system case path. It also deletes bait messages and recovers
// interrupted incidents.
type caseApplier struct {
	cases   CaseCreator
	store   CoreStore
	session *discordgo.Session
}

// ApplyHoneypotCase checks that the request is a complete, system-attributed
// honeypot request and opens its case.
func (a caseApplier) ApplyHoneypotCase(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	if err := checkRequest(request); err != nil {
		return ApplyResult{}, err
	}
	created, err := a.cases.CreateSystemHoneypot(ctx, request.GuildID, caseInput(request))
	if err != nil {
		return ApplyResult{}, err
	}
	if created == nil || created.ID == "" {
		return ApplyResult{}, errors.New("honeypot case creation returned no saved case")
	}
	return ApplyResult{CaseID: created.ID}, nil
}

// checkRequest accepts only a complete request attributed to Quack.
func checkRequest(request ApplyRequest) error {
	if request.Source != SourceHoneypot || request.ActorType != ActorTypeSystem ||
		strings.TrimSpace(request.ActorDiscordUserID) != "" {
		return errors.New("honeypot case attribution is invalid")
	}
	for _, field := range []string{
		request.GuildID, request.TemplateID, request.TargetDiscordUserID,
		request.ContextChannelDiscordID, request.ContextMessageDiscordID,
		request.ContextURL, request.IdempotencyKey,
	} {
		if strings.TrimSpace(field) == "" {
			return errors.New("honeypot case request is incomplete")
		}
	}
	return nil
}

// caseInput is the core case input for request.
func caseInput(request ApplyRequest) quack.CaseInput {
	return quack.CaseInput{
		TemplateID:              request.TemplateID,
		TargetDiscordUserID:     request.TargetDiscordUserID,
		Source:                  quack.CaseSourceHoneypot,
		ContextChannelDiscordID: request.ContextChannelDiscordID,
		ContextMessageDiscordID: request.ContextMessageDiscordID,
		ContextURL:              request.ContextURL,
		IdempotencyKey:          request.IdempotencyKey,
	}
}

// DeleteHoneypotMessage deletes a bait message. A message or channel that is
// already gone counts as deleted, so a replay after a crash between the
// delete and its receipt succeeds.
func (a caseApplier) DeleteHoneypotMessage(ctx context.Context, channelID, messageID string) error {
	err := a.session.ChannelMessageDelete(channelID, messageID, rest(ctx)...)
	if restCode(err) == discordgo.ErrCodeUnknownMessage || restCode(err) == discordgo.ErrCodeUnknownChannel {
		return nil
	}
	return err
}

// FindHoneypotCase returns the case already saved under the request's
// idempotency key, if any, without asking Discord or opening anything.
func (a caseApplier) FindHoneypotCase(ctx context.Context, request ApplyRequest) (ApplyResult, error) {
	saved, err := a.store.GetCaseByIdempotencyKey(ctx, request.GuildID, request.IdempotencyKey)
	if err != nil || saved == nil {
		return ApplyResult{}, err
	}
	if saved.Source != quack.CaseSourceHoneypot || saved.TargetDiscordUserID != request.TargetDiscordUserID {
		return ApplyResult{}, errors.New("honeypot idempotency key belongs to another case")
	}
	return ApplyResult{CaseID: saved.ID}, nil
}

// PrepareHoneypotRecovery re-checks the original message and its author
// live before a missing case goes through the normal case preflight. It
// rebuilds the message link from the live channel, since triggers do not
// store it.
func (a caseApplier) PrepareHoneypotRecovery(ctx context.Context, request ApplyRequest) (ApplyRequest, error) {
	channel, err := a.session.Channel(request.ContextChannelDiscordID, rest(ctx)...)
	if err != nil {
		return request, err
	}
	if channel.ID != request.ContextChannelDiscordID || channel.GuildID == "" || channel.Type != discordgo.ChannelTypeGuildText {
		return request, ErrNotTrigger
	}
	message, err := a.session.ChannelMessage(channel.ID, request.ContextMessageDiscordID, rest(ctx)...)
	if restCode(err) == discordgo.ErrCodeUnknownMessage {
		return request, ErrNotTrigger
	}
	if err != nil {
		return request, err
	}
	if message.ID != request.ContextMessageDiscordID || message.ChannelID != channel.ID ||
		message.Author == nil || message.Author.ID != request.TargetDiscordUserID {
		return request, ErrNotTrigger
	}
	quackID := botID(a.session)
	if message.Author.ID == quackID || message.WebhookID != "" {
		return request, ErrExempt
	}
	guild, err := a.session.Guild(channel.GuildID, rest(ctx)...)
	if err != nil {
		return request, err
	}
	member, err := a.session.GuildMember(channel.GuildID, request.TargetDiscordUserID, rest(ctx)...)
	if err != nil {
		return request, err
	}
	if guild.ID != channel.GuildID || member.User == nil || member.User.ID != request.TargetDiscordUserID {
		return request, errors.New("honeypot recovery member is unavailable")
	}
	if member.User.ID == quackID || canModerate(guild, member) {
		return request, ErrExempt
	}
	request.ContextURL = messageURL(channel.GuildID, channel.ID, message.ID)
	return request, nil
}

// templateValidator is the TemplateValidator over the live template.
type templateValidator struct{ store CoreStore }

// ValidateHoneypotTemplate accepts only a template that can run with nobody
// at the keyboard: active, no required context fields, exactly one default
// level, and at most one timeout, kick, or ban per level. Member DMs come
// from a level's notify_user, not an action. A template that fails is
// reported as ErrTemplateUnavailable; a storage failure is returned as is,
// so it never turns the honeypot off.
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
