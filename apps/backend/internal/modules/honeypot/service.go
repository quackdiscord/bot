package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
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
	return &Service{registry: registry, store: store, auditor: auditor, channels: channels, templates: templates, applier: applier}
}

// Settings returns the guild's settings and status. It needs Manage Guild.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, Status, error) {
	if !actor.CanManage {
		s.audit(ctx, actor.GuildID, actor.DiscordUserID, "honeypot.settings.read", "honeypot_settings", "denied", ErrPermissionDenied, "")
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
	if !actor.CanManage {
		s.audit(ctx, actor.GuildID, actor.DiscordUserID, "honeypot.settings.update", "honeypot_settings", "denied", ErrPermissionDenied, "")
		return Settings{}, Status{}, ErrPermissionDenied
	}
	settings = normalizeSettings(settings)
	settings.DisabledReason = ""
	if err := validateSettings(settings, enabled); err != nil {
		return Settings{}, Status{}, err
	}
	if enabled {
		if s.channels == nil || s.templates == nil {
			return Settings{}, Status{}, errors.New("honeypot validators are not configured")
		}
		if err := s.channels.ValidateHoneypotChannel(ctx, actor.GuildID, settings.ChannelDiscordID); err != nil {
			s.audit(ctx, actor.GuildID, actor.DiscordUserID, "honeypot.settings.update", "honeypot_settings", "failure", err, "")
			return Settings{}, Status{}, fmt.Errorf("%w: %v", ErrChannelUnavailable, err)
		}
		if err := s.templates.ValidateHoneypotTemplate(ctx, actor.GuildID, settings.TemplateID); err != nil {
			s.audit(ctx, actor.GuildID, actor.DiscordUserID, "honeypot.settings.update", "honeypot_settings", "failure", err, "")
			return Settings{}, Status{}, fmt.Errorf("%w: %v", ErrTemplateUnavailable, err)
		}
	}
	if err := s.putSettings(ctx, actor.GuildID, enabled, settings); err != nil {
		return Settings{}, Status{}, err
	}
	s.audit(ctx, actor.GuildID, actor.DiscordUserID, "honeypot.settings.update", "honeypot_settings", "success", nil, "")
	status, err := s.status(ctx, actor.GuildID, settings, enabled)
	return settings, status, err
}

// HandleMessage opens a case for a message in the trap channel, unless the
// author is exempt or the message was already handled. A template that has
// become unusable turns the honeypot off instead.
func (s *Service) HandleMessage(ctx context.Context, message Message) (ApplyResult, error) {
	message = normalizeMessage(message)
	settings, enabled, err := s.loadSettings(ctx, message.GuildID)
	if err != nil {
		return ApplyResult{}, err
	}
	if !enabled {
		return ApplyResult{}, ErrDisabled
	}
	if message.GuildID == "" || message.ChannelDiscordID != settings.ChannelDiscordID || message.MessageDiscordID == "" || message.AuthorDiscordUserID == "" {
		return ApplyResult{}, ErrNotTrigger
	}
	if isExempt(message, settings) {
		_, created, claimErr := s.store.Claim(ctx, message, settings.TemplateID, OutcomeExempt)
		if claimErr != nil {
			return ApplyResult{}, claimErr
		}
		if !created {
			return ApplyResult{}, ErrDuplicate
		}
		return ApplyResult{}, ErrExempt
	}
	trigger, created, err := s.store.Claim(ctx, message, settings.TemplateID, OutcomePending)
	if err != nil {
		return ApplyResult{}, err
	}
	if !created {
		return ApplyResult{}, ErrDuplicate
	}
	s.audit(ctx, message.GuildID, "", "honeypot.trigger.detected", "honeypot_trigger", "success", nil, trigger.ID)
	if s.templates == nil {
		err = errors.New("honeypot template validator is not configured")
	} else {
		err = s.templates.ValidateHoneypotTemplate(ctx, message.GuildID, settings.TemplateID)
	}
	if err != nil {
		_ = s.store.Complete(ctx, trigger.ID, OutcomeFailed, "", "template_unavailable")
		_ = s.disableForDrift(ctx, message.GuildID, settings, "selected template is archived, missing, or incompatible")
		s.audit(ctx, message.GuildID, "", "honeypot.trigger.failed", "honeypot_trigger", "failure", err, trigger.ID)
		return ApplyResult{}, fmt.Errorf("%w: %v", ErrTemplateUnavailable, err)
	}
	request := ApplyRequest{
		GuildID: message.GuildID, TemplateID: settings.TemplateID, TargetDiscordUserID: message.AuthorDiscordUserID,
		ContextChannelDiscordID: message.ChannelDiscordID, ContextMessageDiscordID: message.MessageDiscordID, ContextURL: message.MessageURL,
		IdempotencyKey: "honeypot:" + message.GuildID + ":" + message.MessageDiscordID,
		Source:         SourceHoneypot, ActorType: ActorTypeSystem,
	}
	result, err := s.applier.ApplyHoneypotCase(ctx, request)
	if err != nil {
		_ = s.store.Complete(ctx, trigger.ID, OutcomeFailed, "", "case_application_failed")
		s.audit(ctx, message.GuildID, "", "honeypot.trigger.failed", "honeypot_trigger", "failure", err, trigger.ID)
		return ApplyResult{}, err
	}
	if strings.TrimSpace(result.CaseID) == "" {
		err = errors.New("normal case path returned no case id")
		_ = s.store.Complete(ctx, trigger.ID, OutcomeFailed, "", "invalid_case_result")
		s.audit(ctx, message.GuildID, "", "honeypot.trigger.failed", "honeypot_trigger", "failure", err, trigger.ID)
		return ApplyResult{}, err
	}
	if err := s.store.Complete(ctx, trigger.ID, OutcomeCreated, result.CaseID, ""); err != nil {
		return ApplyResult{}, err
	}
	s.audit(ctx, message.GuildID, "", "honeypot.case.created", "case", "success", nil, result.CaseID)
	return result, nil
}

// HandleDeletedChannel turns the honeypot off if channelID was its trap,
// keeping the settings so Repair can turn it back on.
func (s *Service) HandleDeletedChannel(ctx context.Context, guildID, channelID string) error {
	settings, enabled, err := s.loadSettings(ctx, strings.TrimSpace(guildID))
	if err != nil || !enabled || settings.ChannelDiscordID != strings.TrimSpace(channelID) {
		return err
	}
	return s.disableForDrift(ctx, guildID, settings, "configured honeypot channel was deleted")
}

// HandleTemplateUnavailable turns the honeypot off if it uses templateID,
// keeping the settings so Repair can turn it back on.
func (s *Service) HandleTemplateUnavailable(ctx context.Context, guildID, templateID string) error {
	settings, enabled, err := s.loadSettings(ctx, strings.TrimSpace(guildID))
	if err != nil || !enabled || settings.TemplateID != strings.TrimSpace(templateID) {
		return err
	}
	return s.disableForDrift(ctx, guildID, settings, "selected template is archived, missing, or incompatible")
}

// Repair turns the honeypot back on with its kept settings, checking them
// live first.
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

func (s *Service) disableForDrift(ctx context.Context, guildID string, settings Settings, reason string) error {
	settings.DisabledReason = reason
	if err := s.putSettings(ctx, guildID, false, settings); err != nil {
		return err
	}
	s.audit(ctx, guildID, "", "honeypot.configuration.disabled", "honeypot_settings", "failure", errors.New(reason), "")
	return nil
}

func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	configuration, err := s.registry.Configuration(ctx, strings.TrimSpace(guildID), modules.Honeypots)
	if err != nil {
		return Settings{}, false, err
	}
	if configuration == nil {
		return Settings{}, false, nil
	}
	var settings Settings
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil {
		return Settings{}, false, err
	}
	return normalizeSettings(settings), configuration.Enabled, nil
}

func (s *Service) putSettings(ctx context.Context, guildID string, enabled bool, settings Settings) error {
	payload, err := json.Marshal(normalizeSettings(settings))
	if err != nil {
		return err
	}
	_, err = s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: guildID, ModuleID: modules.Honeypots, Enabled: enabled, ConfigJSON: string(payload)})
	return err
}

func (s *Service) status(ctx context.Context, guildID string, settings Settings, enabled bool) (Status, error) {
	statistics, err := s.store.Statistics(ctx, guildID)
	if err != nil {
		return Status{}, err
	}
	return Status{Enabled: enabled, Configured: settings.ChannelDiscordID != "" && settings.TemplateID != "", ChannelDiscordID: settings.ChannelDiscordID, TemplateID: settings.TemplateID, DisabledReason: settings.DisabledReason, Statistics: statistics}, nil
}

// audit logs and records a honeypot operation. Automated events have no
// actor.
func (s *Service) audit(ctx context.Context, guildID, actorID, action, resourceType, result string, cause error, resourceID string) {
	failure := ""
	if cause != nil {
		failure = cause.Error()
	}
	modules.Audit(ctx, s.auditor, "honeypot", modules.AuditEvent{
		GuildID: guildID, ActorDiscordUserID: actorID,
		Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Result: result, FailureReason: failure,
	})
}

func normalizeSettings(settings Settings) Settings {
	settings.ChannelDiscordID = strings.TrimSpace(settings.ChannelDiscordID)
	settings.TemplateID = strings.TrimSpace(settings.TemplateID)
	settings.DisabledReason = strings.TrimSpace(settings.DisabledReason)
	for i := range settings.ExemptRoleDiscordIDs {
		settings.ExemptRoleDiscordIDs[i] = strings.TrimSpace(settings.ExemptRoleDiscordIDs[i])
	}
	return settings
}

func normalizeMessage(message Message) Message {
	message.GuildID = strings.TrimSpace(message.GuildID)
	message.ChannelDiscordID = strings.TrimSpace(message.ChannelDiscordID)
	message.MessageDiscordID = strings.TrimSpace(message.MessageDiscordID)
	message.AuthorDiscordUserID = strings.TrimSpace(message.AuthorDiscordUserID)
	return message
}

func isExempt(message Message, settings Settings) bool {
	if message.IsBot || message.IsQuack || message.IsWebhook || message.AuthorCanModerate {
		return true
	}
	exempt := make(map[string]struct{}, len(settings.ExemptRoleDiscordIDs))
	for _, roleID := range settings.ExemptRoleDiscordIDs {
		exempt[roleID] = struct{}{}
	}
	for _, roleID := range message.AuthorRoleDiscordIDs {
		if _, ok := exempt[roleID]; ok {
			return true
		}
	}
	return false
}
