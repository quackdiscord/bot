package honeypot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
)

// Reasons recorded when Quack turns a honeypot off on its own. The settings
// are kept so Repair can turn it back on.
const (
	reasonChannelDeleted      = "configured honeypot channel was deleted"
	reasonTemplateUnavailable = "selected template is archived, missing, or incompatible"
)

// Service applies the trap policy to messages and manages each guild's
// honeypot settings.
type Service struct {
	registry  *modules.Registry
	store     *Store
	auditor   modules.Auditor
	channels  ChannelValidator
	templates TemplateValidator
	applier   CaseApplier
}

// NewService returns a Service. A nil auditor only logs operations.
func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor, channels ChannelValidator, templates TemplateValidator, applier CaseApplier) *Service {
	return &Service{
		registry:  registry,
		store:     store,
		auditor:   auditor,
		channels:  channels,
		templates: templates,
		applier:   applier,
	}
}

// Settings returns the guild's settings and status. It needs Manage Guild.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, Status, error) {
	if !actor.CanManage {
		s.auditSettings(ctx, actor, "honeypot.settings.read", "denied", ErrPermissionDenied)
		return Settings{}, Status{}, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return Settings{}, Status{}, err
	}
	status, err := s.status(ctx, actor.GuildID, settings, enabled)
	if err != nil {
		return Settings{}, Status{}, err
	}
	return settings, status, nil
}

// UpdateSettings saves the guild's settings. Turning the honeypot on checks
// the channel and template live first. It needs Manage Guild.
func (s *Service) UpdateSettings(ctx context.Context, actor modules.Actor, enabled bool, settings Settings) (Settings, Status, error) {
	const action = "honeypot.settings.update"
	if !actor.CanManage {
		s.auditSettings(ctx, actor, action, "denied", ErrPermissionDenied)
		return Settings{}, Status{}, ErrPermissionDenied
	}
	settings = normalizeSettings(settings)
	settings.DisabledReason = ""
	if err := validateSettings(settings, enabled); err != nil {
		return Settings{}, Status{}, err
	}
	if enabled {
		if err := s.channels.ValidateHoneypotChannel(ctx, actor.GuildID, settings.ChannelDiscordID); err != nil {
			s.auditSettings(ctx, actor, action, "failure", err)
			return Settings{}, Status{}, fmt.Errorf("%w: %v", ErrChannelUnavailable, err)
		}
		if err := s.templates.ValidateHoneypotTemplate(ctx, actor.GuildID, settings.TemplateID); err != nil {
			s.auditSettings(ctx, actor, action, "failure", err)
			return Settings{}, Status{}, fmt.Errorf("%w: %v", ErrTemplateUnavailable, err)
		}
	}
	if err := s.saveSettings(ctx, actor.GuildID, enabled, settings); err != nil {
		return Settings{}, Status{}, err
	}
	s.auditSettings(ctx, actor, action, "success", nil)
	status, err := s.status(ctx, actor.GuildID, settings, enabled)
	return settings, status, err
}

// Repair turns the honeypot back on with its kept settings, checking them
// live first. It needs Manage Guild.
func (s *Service) Repair(ctx context.Context, actor modules.Actor) (Settings, Status, error) {
	if !actor.CanManage {
		return Settings{}, Status{}, ErrPermissionDenied
	}
	settings, _, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return Settings{}, Status{}, err
	}
	return s.UpdateSettings(ctx, actor, true, settings)
}

// HandleMessage opens a case for a message in the trap channel, unless the
// author is exempt or the message was already handled. The trigger is
// claimed before anything acts on it, so each message opens at most one
// case. A template that has become unusable turns the honeypot off instead.
func (s *Service) HandleMessage(ctx context.Context, message Message) (ApplyResult, error) {
	message = normalizeMessage(message)
	settings, enabled, err := s.loadSettings(ctx, message.GuildID)
	if err != nil {
		return ApplyResult{}, err
	}
	if !enabled {
		return ApplyResult{}, ErrDisabled
	}
	if message.GuildID == "" || message.ChannelDiscordID != settings.ChannelDiscordID ||
		message.MessageDiscordID == "" || message.AuthorDiscordUserID == "" {
		return ApplyResult{}, ErrNotTrigger
	}
	if isExempt(message, settings) {
		_, claimed, err := s.store.Claim(ctx, message, settings.TemplateID, OutcomeExempt)
		switch {
		case err != nil:
			return ApplyResult{}, err
		case !claimed:
			return ApplyResult{}, ErrDuplicate
		default:
			return ApplyResult{}, ErrExempt
		}
	}
	trigger, claimed, err := s.store.Claim(ctx, message, settings.TemplateID, OutcomePending)
	if err != nil {
		return ApplyResult{}, err
	}
	if !claimed {
		return ApplyResult{}, ErrDuplicate
	}
	s.auditTrigger(ctx, message.GuildID, "honeypot.trigger.detected", "honeypot_trigger", trigger.ID, nil)

	if err := s.templates.ValidateHoneypotTemplate(ctx, message.GuildID, settings.TemplateID); err != nil {
		_ = s.store.Complete(ctx, trigger.ID, OutcomeFailed, "", "template_unavailable")
		_ = s.disable(ctx, message.GuildID, settings, reasonTemplateUnavailable)
		s.auditTrigger(ctx, message.GuildID, "honeypot.trigger.failed", "honeypot_trigger", trigger.ID, err)
		return ApplyResult{}, fmt.Errorf("%w: %v", ErrTemplateUnavailable, err)
	}
	result, err := s.applier.ApplyHoneypotCase(ctx, ApplyRequest{
		GuildID:                 message.GuildID,
		TemplateID:              settings.TemplateID,
		TargetDiscordUserID:     message.AuthorDiscordUserID,
		ContextChannelDiscordID: message.ChannelDiscordID,
		ContextMessageDiscordID: message.MessageDiscordID,
		ContextURL:              message.MessageURL,
		IdempotencyKey:          "honeypot:" + message.GuildID + ":" + message.MessageDiscordID,
		Source:                  SourceHoneypot,
		ActorType:               ActorTypeSystem,
	})
	if err == nil && strings.TrimSpace(result.CaseID) == "" {
		return ApplyResult{}, s.failTrigger(ctx, message.GuildID, trigger.ID, "invalid_case_result",
			errors.New("normal case path returned no case id"))
	}
	if err != nil {
		return ApplyResult{}, s.failTrigger(ctx, message.GuildID, trigger.ID, "case_application_failed", err)
	}
	if err := s.store.Complete(ctx, trigger.ID, OutcomeCreated, result.CaseID, ""); err != nil {
		return ApplyResult{}, err
	}
	s.auditTrigger(ctx, message.GuildID, "honeypot.case.created", "case", result.CaseID, nil)
	return result, nil
}

// HandleDeletedChannel turns the honeypot off if channelID was its trap.
func (s *Service) HandleDeletedChannel(ctx context.Context, guildID, channelID string) error {
	settings, enabled, err := s.loadSettings(ctx, guildID)
	if err != nil || !enabled || settings.ChannelDiscordID != strings.TrimSpace(channelID) {
		return err
	}
	return s.disable(ctx, guildID, settings, reasonChannelDeleted)
}

// HandleTemplateUnavailable turns the honeypot off if it uses templateID.
func (s *Service) HandleTemplateUnavailable(ctx context.Context, guildID, templateID string) error {
	settings, enabled, err := s.loadSettings(ctx, guildID)
	if err != nil || !enabled || settings.TemplateID != strings.TrimSpace(templateID) {
		return err
	}
	return s.disable(ctx, guildID, settings, reasonTemplateUnavailable)
}

// disable turns the honeypot off for reason, keeping its settings.
func (s *Service) disable(ctx context.Context, guildID string, settings Settings, reason string) error {
	settings.DisabledReason = reason
	if err := s.saveSettings(ctx, guildID, false, settings); err != nil {
		return err
	}
	modules.Audit(ctx, s.auditor, "honeypot", modules.AuditEvent{
		GuildID:       guildID,
		Action:        "honeypot.configuration.disabled",
		ResourceType:  "honeypot_settings",
		Result:        "failure",
		FailureReason: reason,
	})
	return nil
}

// failTrigger records that a claimed trigger opened no case, and returns
// cause.
func (s *Service) failTrigger(ctx context.Context, guildID, triggerID, code string, cause error) error {
	_ = s.store.Complete(ctx, triggerID, OutcomeFailed, "", code)
	s.auditTrigger(ctx, guildID, "honeypot.trigger.failed", "honeypot_trigger", triggerID, cause)
	return cause
}

// loadSettings returns the guild's settings, normalized, and whether the
// honeypot is on.
func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	settings, enabled, err := modules.LoadSettings(ctx, s.registry, strings.TrimSpace(guildID), modules.Honeypots, Settings{})
	if err != nil {
		return Settings{}, false, err
	}
	return normalizeSettings(settings), enabled, nil
}

func (s *Service) saveSettings(ctx context.Context, guildID string, enabled bool, settings Settings) error {
	_, err := s.registry.SaveSettings(ctx, guildID, modules.Honeypots, enabled, normalizeSettings(settings))
	return err
}

// status summarizes the guild's honeypot for managers.
func (s *Service) status(ctx context.Context, guildID string, settings Settings, enabled bool) (Status, error) {
	statistics, err := s.store.Statistics(ctx, guildID)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Enabled:          enabled,
		Configured:       settings.ChannelDiscordID != "" && settings.TemplateID != "",
		ChannelDiscordID: settings.ChannelDiscordID,
		TemplateID:       settings.TemplateID,
		DisabledReason:   settings.DisabledReason,
		Statistics:       statistics,
	}, nil
}

// auditSettings logs and records a manager's settings operation.
func (s *Service) auditSettings(ctx context.Context, actor modules.Actor, action, result string, cause error) {
	modules.Audit(ctx, s.auditor, "honeypot", modules.AuditEvent{
		GuildID:            actor.GuildID,
		ActorDiscordUserID: actor.DiscordUserID,
		Action:             action,
		ResourceType:       "honeypot_settings",
		Result:             result,
		FailureReason:      reason(cause),
	})
}

// auditTrigger logs and records a step of handling a trap message. These
// steps have no actor: Quack takes them on its own.
func (s *Service) auditTrigger(ctx context.Context, guildID, action, resourceType, resourceID string, cause error) {
	result := "success"
	if cause != nil {
		result = "failure"
	}
	modules.Audit(ctx, s.auditor, "honeypot", modules.AuditEvent{
		GuildID:       guildID,
		Action:        action,
		ResourceType:  resourceType,
		ResourceID:    resourceID,
		Result:        result,
		FailureReason: reason(cause),
	})
}

// reason is the audit failure reason for cause.
func reason(cause error) string {
	if cause == nil {
		return ""
	}
	return cause.Error()
}

// normalizeSettings trims the IDs a manager typed.
func normalizeSettings(settings Settings) Settings {
	settings.ChannelDiscordID = strings.TrimSpace(settings.ChannelDiscordID)
	settings.TemplateID = strings.TrimSpace(settings.TemplateID)
	settings.DisabledReason = strings.TrimSpace(settings.DisabledReason)
	for i := range settings.ExemptRoleDiscordIDs {
		settings.ExemptRoleDiscordIDs[i] = strings.TrimSpace(settings.ExemptRoleDiscordIDs[i])
	}
	return settings
}

// normalizeMessage trims a message's IDs before they are compared with the
// settings.
func normalizeMessage(message Message) Message {
	message.GuildID = strings.TrimSpace(message.GuildID)
	message.ChannelDiscordID = strings.TrimSpace(message.ChannelDiscordID)
	message.MessageDiscordID = strings.TrimSpace(message.MessageDiscordID)
	message.AuthorDiscordUserID = strings.TrimSpace(message.AuthorDiscordUserID)
	return message
}

// isExempt reports whether the author is one the trap never fires on: a
// bot, Quack, a webhook, staff, or a member with an exempt role.
func isExempt(message Message, settings Settings) bool {
	if message.IsBot || message.IsQuack || message.IsWebhook || message.AuthorCanModerate {
		return true
	}
	return slices.ContainsFunc(message.AuthorRoleDiscordIDs, func(roleID string) bool {
		return slices.Contains(settings.ExemptRoleDiscordIDs, roleID)
	})
}
