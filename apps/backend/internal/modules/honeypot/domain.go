// Package honeypot is the optional trap-channel module. Admins pick a
// channel no real member should post in and a template; when someone posts
// there, Quack opens a case against them with that template, through the
// normal case path, attributed to Quack itself, and then deletes the bait
// message. A warning post in the channel says what will happen and counts
// the incidents caught. Staff, Quack, and webhooks are ignored; ordinary
// bots are not. A burst of messages from one member is one incident, and
// each message triggers at most once, even across restarts.
package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

var (
	// ErrDisabled reports a guild with the honeypot off.
	ErrDisabled = errors.New("honeypot module is disabled")
	// ErrPermissionDenied reports a caller without Manage Guild.
	ErrPermissionDenied = errors.New("honeypot permission denied")
	// ErrDuplicate reports a message that was already handled, or one that
	// joined an incident already under way.
	ErrDuplicate = errors.New("honeypot message already handled")
	// ErrExempt reports a message whose author is exempt.
	ErrExempt = errors.New("honeypot author is exempt")
	// ErrNotTrigger reports a message outside the trap channel, or one the
	// current settings no longer cover.
	ErrNotTrigger = errors.New("message does not qualify for honeypot processing")
	// ErrChannelUnavailable reports a trap channel that is gone or that
	// Quack cannot use.
	ErrChannelUnavailable = errors.New("honeypot channel is unavailable")
	// ErrTemplateUnavailable reports a template that is archived, missing, or
	// cannot run unattended. Only this error turns the honeypot off; a
	// storage failure does not.
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
	// WarningMessageID is the warning post in the trap channel. Quack keeps
	// it up to date and posts a new one if it is deleted.
	WarningMessageID string `json:"warning_message_id,omitempty"`
	// WarningText is the admin's own warning. Empty means Quack words it
	// from the template's punishments.
	WarningText      string `json:"warning_text,omitempty"`
	ChannelDiscordID string `json:"channel_discord_id"`
	TemplateID       string `json:"template_id"`
	DisabledReason   string `json:"disabled_reason,omitempty"`
}

// Message is what the trap policy needs to know about a message.
type Message struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID string
	MessageURL                                                       string
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

// CaseApplier opens honeypot cases. An applier that also implements
// MessageCleaner deletes bait messages, and one that implements
// IncidentRecoverer finishes incidents a restart interrupted.
type CaseApplier interface {
	ApplyHoneypotCase(ctx context.Context, request ApplyRequest) (ApplyResult, error)
}

// MessageCleaner deletes a bait message once its incident has a saved case.
// A message or channel that is already gone counts as deleted, so a replay
// after a crash is harmless.
type MessageCleaner interface {
	DeleteHoneypotMessage(ctx context.Context, channelID, messageID string) error
}

// IncidentRecoverer finishes an incident whose worker died. Find looks up a
// case already saved under the request's idempotency key without touching
// Discord; Prepare re-checks the message and its author live before a
// missing case is opened, returning ErrExempt or ErrNotTrigger when it must
// not be.
type IncidentRecoverer interface {
	FindHoneypotCase(ctx context.Context, request ApplyRequest) (ApplyResult, error)
	PrepareHoneypotRecovery(ctx context.Context, request ApplyRequest) (ApplyRequest, error)
}

// TemplateValidator checks that a template can run unattended. guildID is
// the internal guild ID.
type TemplateValidator interface {
	ValidateHoneypotTemplate(ctx context.Context, guildID, templateID string) error
}

// ChannelValidator checks that Quack can use a trap channel. guildID is the
// internal guild ID.
type ChannelValidator interface {
	ValidateHoneypotChannel(ctx context.Context, guildID, channelID string) error
}

// Outcome is what came of a trigger. A trigger is claimed as pending (or
// straight away as exempt) and completed once as created or failed.
type Outcome string

// The outcomes a trigger can record.
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
	settings = normalizeSettings(settings)
	if enabled && (settings.ChannelDiscordID == "" || settings.TemplateID == "") {
		return errors.New("enabled honeypots require a channel and active template")
	}
	return nil
}

// decodeEnabledSettings decodes stored config JSON and checks it is complete
// enough to switch on.
func decodeEnabledSettings(raw string) (Settings, error) {
	var settings Settings
	if err := json.Unmarshal([]byte(raw), &settings); err != nil {
		return Settings{}, err
	}
	settings = normalizeSettings(settings)
	return settings, validateSettings(settings, true)
}

// normalizeSettings trims the IDs a manager typed.
func normalizeSettings(settings Settings) Settings {
	settings.ChannelDiscordID = strings.TrimSpace(settings.ChannelDiscordID)
	settings.TemplateID = strings.TrimSpace(settings.TemplateID)
	settings.DisabledReason = strings.TrimSpace(settings.DisabledReason)
	settings.WarningMessageID = strings.TrimSpace(settings.WarningMessageID)
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

// isExempt reports whether the author is one the trap never fires on:
// Quack, a webhook, or a member with guild-wide moderation authority.
// Ordinary bots are caught like anyone else; spam bots are the point.
func isExempt(message Message) bool {
	return message.IsQuack || message.IsWebhook || message.AuthorCanModerate
}
