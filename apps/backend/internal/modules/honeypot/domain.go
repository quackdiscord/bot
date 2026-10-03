// Package honeypot is the optional trap-channel module. Admins pick a
// channel no real member should post in and a template; when someone posts
// there, Quack opens a case against them with that template, through the
// normal case path, attributed to Quack itself. Bots, webhooks, staff, and
// exempt roles are ignored, and each message triggers at most once.
package honeypot

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrDisabled reports a guild with the honeypot off.
	ErrDisabled = errors.New("honeypot module is disabled")
	// ErrPermissionDenied reports a caller without Manage Guild.
	ErrPermissionDenied = errors.New("honeypot permission denied")
	// ErrDuplicate reports a message that was already handled.
	ErrDuplicate = errors.New("honeypot message already handled")
	// ErrExempt reports a message whose author is exempt.
	ErrExempt = errors.New("honeypot author is exempt")
	// ErrNotTrigger reports a message outside the trap channel.
	ErrNotTrigger = errors.New("message does not qualify for honeypot processing")
	// ErrChannelUnavailable reports a trap channel that is gone or that
	// Quack cannot see.
	ErrChannelUnavailable = errors.New("honeypot channel is unavailable")
	// ErrTemplateUnavailable reports a template that is archived, missing, or
	// cannot run unattended.
	ErrTemplateUnavailable = errors.New("honeypot template is unavailable")
)

const (
	// SourceHoneypot is the source of every honeypot case.
	SourceHoneypot = "honeypot"
	// ActorTypeSystem marks a case opened by Quack, with no staff actor.
	ActorTypeSystem = "system"
)

// Settings are a guild's honeypot settings, stored as the module's config
// JSON.
type Settings struct {
	ChannelDiscordID     string   `json:"channel_discord_id"`
	TemplateID           string   `json:"template_id"`
	ExemptRoleDiscordIDs []string `json:"exempt_role_discord_ids,omitempty"`
	DisabledReason       string   `json:"disabled_reason,omitempty"`
}

// Message is what the trap policy needs to know about a message.
type Message struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID string
	MessageURL                                                       string
	AuthorRoleDiscordIDs                                             []string
	IsBot, IsQuack, IsWebhook, AuthorCanModerate                     bool
}

// ApplyRequest asks for a case to be opened through the normal case path.
type ApplyRequest struct {
	GuildID, TemplateID, TargetDiscordUserID                     string
	ContextChannelDiscordID, ContextMessageDiscordID, ContextURL string
	IdempotencyKey, Source, ActorType, ActorDiscordUserID        string
}

// ApplyResult is the case that was opened.
type ApplyResult struct {
	CaseID string
}

// CaseApplier opens honeypot cases.
type CaseApplier interface {
	ApplyHoneypotCase(context.Context, ApplyRequest) (ApplyResult, error)
}

// TemplateValidator checks that a template can run unattended.
type TemplateValidator interface {
	ValidateHoneypotTemplate(context.Context, string, string) error
}

// ChannelValidator checks that Quack can see a trap channel.
type ChannelValidator interface {
	ValidateHoneypotChannel(context.Context, string, string) error
}

// Outcome is what came of a trigger.
type Outcome string

const (
	OutcomePending Outcome = "pending"
	OutcomeCreated Outcome = "created"
	OutcomeFailed  Outcome = "failed"
	OutcomeExempt  Outcome = "exempt"
)

// Statistics are a guild's trigger counts by outcome.
type Statistics struct {
	Total   uint64 `json:"total"`
	Pending uint64 `json:"pending"`
	Created uint64 `json:"created"`
	Failed  uint64 `json:"failed"`
	Exempt  uint64 `json:"exempt"`
}

// Status is a guild's honeypot state as managers see it.
type Status struct {
	Enabled          bool       `json:"enabled"`
	Configured       bool       `json:"configured"`
	ChannelDiscordID string     `json:"channel_discord_id,omitempty"`
	TemplateID       string     `json:"template_id,omitempty"`
	DisabledReason   string     `json:"disabled_reason,omitempty"`
	Statistics       Statistics `json:"statistics"`
}

// validateSettings checks settings; an enabled honeypot also needs a
// channel and a template.
func validateSettings(settings Settings, enabled bool) error {
	settings.ChannelDiscordID = strings.TrimSpace(settings.ChannelDiscordID)
	settings.TemplateID = strings.TrimSpace(settings.TemplateID)
	if enabled && (settings.ChannelDiscordID == "" || settings.TemplateID == "") {
		return errors.New("enabled honeypots require a channel and active template")
	}
	seen := make(map[string]struct{}, len(settings.ExemptRoleDiscordIDs))
	for _, roleID := range settings.ExemptRoleDiscordIDs {
		roleID = strings.TrimSpace(roleID)
		if roleID == "" {
			return errors.New("exempt role ids cannot be empty")
		}
		if _, ok := seen[roleID]; ok {
			return errors.New("exempt role ids must be unique")
		}
		seen[roleID] = struct{}{}
	}
	return nil
}
