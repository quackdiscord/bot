package honeypot

import (
	"context"
	"errors"
	"fmt"
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
// author is exempt, the message was already handled, or it joined the
// member's incident already under way (ErrDuplicate). The incident is
// claimed, and the message queued for cleanup, before anything acts on it,
// so each incident opens at most one case. A template that has become
// unusable turns the honeypot off; a storage failure only fails the
// incident.
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
	if isExempt(message) {
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
	trigger, claimed, err := s.store.ClaimIncident(ctx, message, settings.TemplateID)
	if err != nil {
		return ApplyResult{}, err
	}
	if !claimed {
		return ApplyResult{}, ErrDuplicate
	}
	// Give up before the lease expires and recovery takes over. A cancelled
	// completion leaves the incident pending for recovery, which looks for
	// a saved case first.
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	s.auditTrigger(ctx, message.GuildID, "honeypot.trigger.detected", "honeypot_trigger", trigger.ID, nil)

	if err := s.templates.ValidateHoneypotTemplate(ctx, message.GuildID, settings.TemplateID); err != nil {
		_ = s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "template_unavailable")
		if errors.Is(err, ErrTemplateUnavailable) {
			_ = s.disable(ctx, message.GuildID, settings, reasonTemplateUnavailable)
		}
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
		IdempotencyKey:          idempotencyKey(message.GuildID, message.MessageDiscordID),
		Source:                  SourceHoneypot,
		ActorType:               ActorTypeSystem,
	})
	if err == nil && strings.TrimSpace(result.CaseID) == "" {
		err = errors.New("normal case path returned no case id")
		return ApplyResult{}, s.failTrigger(ctx, message.GuildID, trigger, "invalid_case_result", err)
	}
	if err != nil {
		return ApplyResult{}, s.failTrigger(ctx, message.GuildID, trigger, "case_application_failed", err)
	}
	if err := s.store.completeIncident(ctx, trigger, OutcomeCreated, result.CaseID, ""); err != nil {
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

// idempotencyKey is the case idempotency key for a trap message. Recovery
// rebuilds it to find a case the first attempt may have saved.
func idempotencyKey(guildID, messageID string) string {
	return "honeypot:" + guildID + ":" + messageID
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

// failTrigger records that a claimed incident opened no case, and returns
// cause. The failed incident does not absorb the member's next message.
func (s *Service) failTrigger(ctx context.Context, guildID string, trigger *Trigger, code string, cause error) error {
	_ = s.store.completeIncident(ctx, trigger, OutcomeFailed, "", code)
	s.auditTrigger(ctx, guildID, "honeypot.trigger.failed", "honeypot_trigger", trigger.ID, cause)
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
