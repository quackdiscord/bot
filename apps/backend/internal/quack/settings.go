package quack

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// maxNotificationBrandingLength bounds the guild text added to case
// notifications. Rendering truncates further; this only rejects abuse.
const maxNotificationBrandingLength = 2000

// ModuleStates says which optional modules a guild has switched on.
type ModuleStates struct {
	Tickets, GeneralLogging, Honeypot bool
}

// ModuleToggles reads and writes the optional modules' on/off switches. The
// switches live with each module's configuration, which is what the modules
// themselves read, so the settings API must go through this port rather
// than store a copy.
type ModuleToggles interface {
	ModuleStates(ctx context.Context, guildID string) (ModuleStates, error)
	SetModuleStates(ctx context.Context, guildID string, states ModuleStates) error
}

// GuildSettingsService reads and updates a guild's core settings. All access
// needs Manage Guild and is audited.
type GuildSettingsService struct {
	store    SettingsStore
	channels StaffChannelValidator
	modules  ModuleToggles
}

// NewGuildSettingsService returns a GuildSettingsService. Without channels,
// setting an audit channel fails validation. Without modules, every module
// reads as off and switching one on fails validation.
func NewGuildSettingsService(store SettingsStore, channels StaffChannelValidator, modules ModuleToggles) *GuildSettingsService {
	return &GuildSettingsService{store: store, channels: channels, modules: modules}
}

// GuildSettingsInput is a partial settings update. Nil fields are left
// unchanged.
type GuildSettingsInput struct {
	// AppealQueueChannelDiscordID must pass StaffChannelValidator when set.
	AppealQueueChannelDiscordID *string `json:"appeal_queue_channel_discord_id"`
	// AppealRejoinURL must be an https Discord invite; it is stored as
	// https://discord.gg/<code>.
	AppealRejoinURL             *string `json:"appeal_rejoin_url"`
	AppealReviewReasonRequired  *bool   `json:"appeal_review_reason_required"`
	AuditMirrorChannelDiscordID *string `json:"audit_mirror_channel_discord_id"`
	// ManagedEvidenceChannelDiscordID is rejected when set: Quack owns that
	// channel.
	ManagedEvidenceChannelDiscordID *string `json:"managed_evidence_channel_discord_id"`
	NotificationIntroduction        *string `json:"notification_introduction"`
	NotificationFooter              *string `json:"notification_footer"`
	// The module switches are stored with the modules, not in guild_settings.
	TicketsEnabled        *bool `json:"tickets_enabled"`
	GeneralLoggingEnabled *bool `json:"general_logging_enabled"`
	HoneypotEnabled       *bool `json:"honeypot_enabled"`
}

// GuildSettingsResponse is a guild's settings as the dashboard sees them.
type GuildSettingsResponse struct {
	ID                                string     `json:"id"`
	GuildID                           string     `json:"guild_id"`
	AppealQueueChannelDiscordID       string     `json:"appeal_queue_channel_discord_id,omitempty"`
	AppealRejoinURL                   string     `json:"appeal_rejoin_url,omitempty"`
	AppealReviewReasonRequired        bool       `json:"appeal_review_reason_required"`
	AuditMirrorChannelDiscordID       string     `json:"audit_mirror_channel_discord_id,omitempty"`
	ManagedEvidenceChannelDiscordID   string     `json:"managed_evidence_channel_discord_id,omitempty"`
	NotificationIntroduction          string     `json:"notification_introduction,omitempty"`
	NotificationFooter                string     `json:"notification_footer,omitempty"`
	TicketsEnabled                    bool       `json:"tickets_enabled"`
	GeneralLoggingEnabled             bool       `json:"general_logging_enabled"`
	HoneypotEnabled                   bool       `json:"honeypot_enabled"`
	StarterPolicyTemplateID           string     `json:"starter_policy_template_id"`
	StarterPolicyReviewRequired       bool       `json:"starter_policy_review_required"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `json:"starter_policy_notice_acknowledged_at,omitempty"`
}

// Get returns the guild's settings.
func (s *GuildSettingsService) Get(ctx context.Context, guildContext *GuildStaffContext) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionSettingsRead)
	if guildContext == nil || guildContext.Guild == nil {
		return nil, errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsRead) {
		_ = s.audit(ctx, guildContext, action, AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, "query_failed")
		return nil, err
	}
	if settings == nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, "not_found")
		return nil, ErrGuildSettingsNotFound
	}
	states, err := s.moduleStates(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, "query_failed")
		return nil, err
	}
	if err := s.audit(ctx, guildContext, action, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	response := guildSettingsResponse(*settings, states)
	return &response, nil
}

// Update applies a partial settings update. A new audit or appeal queue
// channel must pass StaffChannelValidator.
func (s *GuildSettingsService) Update(ctx context.Context, guildContext *GuildStaffContext, input GuildSettingsInput) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionSettingsUpdate)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, action, AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	if settings == nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, ErrGuildSettingsNotFound.Error())
		return nil, ErrGuildSettingsNotFound
	}
	if err := applyGuildSettingsInput(settings, input); err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	states, err := s.moduleStates(ctx, guildContext.Guild.ID)
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	wantStates := applyModuleInput(states, input)
	if wantStates != states && s.modules == nil {
		err := settingsValidationError("optional modules are unavailable")
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	staffChannels := []struct {
		changed   bool
		channelID string
		problem   string
	}{
		{input.AuditMirrorChannelDiscordID != nil, settings.AuditMirrorChannelDiscordID, "audit channel must be private and belong to this guild"},
		{input.AppealQueueChannelDiscordID != nil, settings.AppealQueueChannelDiscordID, "appeal queue channel must be private and belong to this guild"},
	}
	for _, channel := range staffChannels {
		if !channel.changed || channel.channelID == "" {
			continue
		}
		if s.channels == nil {
			return nil, settingsValidationError("channel validation unavailable")
		}
		if err := s.channels.ValidateStaffChannel(ctx, guildContext.Guild.DiscordGuildID, channel.channelID); err != nil {
			return nil, settingsValidationError(channel.problem)
		}
	}
	updated, err := s.store.UpdateGuildSettings(ctx, UpdateGuildSettingsParams{
		Settings: *settings,
		Audit:    settingsAudit(ctx, guildContext, action),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	if wantStates != states {
		if err := s.modules.SetModuleStates(ctx, guildContext.Guild.ID, wantStates); err != nil {
			_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
			return nil, err
		}
	}
	response := guildSettingsResponse(*updated, wantStates)
	return &response, nil
}

// moduleStates returns the guild's module switches, all off when no modules
// are wired.
func (s *GuildSettingsService) moduleStates(ctx context.Context, guildID string) (ModuleStates, error) {
	if s.modules == nil {
		return ModuleStates{}, nil
	}
	return s.modules.ModuleStates(ctx, guildID)
}

// RejectUpdatePayload audits a settings update the adapter could not decode
// and returns the validation error to send back. Permission is checked first
// so an unauthorized caller learns nothing about the payload.
func (s *GuildSettingsService) RejectUpdatePayload(ctx context.Context, guildContext *GuildStaffContext, payloadErr error) error {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionSettingsUpdate)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, action, AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return ErrGuildSettingsPermissionDenied
	}
	reason := "invalid guild settings payload"
	if payloadErr != nil {
		reason = payloadErr.Error()
	}
	err := settingsValidationError(reason)
	_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
	return err
}

// AcknowledgeStarterPolicyNotice dismisses the one-time "review your starter
// template" notice. The starter template itself is untouched.
func (s *GuildSettingsService) AcknowledgeStarterPolicyNotice(ctx context.Context, guildContext *GuildStaffContext) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionStarterNoticeAcknowledge)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsWrite) {
		_ = s.audit(ctx, guildContext, action, AuditResultDenied, ErrGuildSettingsPermissionDenied.Error())
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, ErrGuildSettingsNotFound
	}
	if settings.StarterPolicyNoticePending {
		now := time.Now().UTC()
		settings.StarterPolicyNoticePending = false
		settings.StarterPolicyNoticeAcknowledgedAt = &now
	}
	states, err := s.moduleStates(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	updated, err := s.store.UpdateGuildSettings(ctx, UpdateGuildSettingsParams{
		Settings: *settings,
		Audit:    settingsAudit(ctx, guildContext, action),
	})
	if err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	response := guildSettingsResponse(*updated, states)
	return &response, nil
}

func applyGuildSettingsInput(settings *GuildSettings, input GuildSettingsInput) error {
	if input.AppealQueueChannelDiscordID != nil {
		value, err := normalizeChannelID(*input.AppealQueueChannelDiscordID)
		if err != nil {
			return err
		}
		settings.AppealQueueChannelDiscordID = value
	}
	if input.AppealRejoinURL != nil {
		value, err := normalizeRejoinURL(*input.AppealRejoinURL)
		if err != nil {
			return err
		}
		settings.AppealRejoinURL = value
	}
	if input.AppealReviewReasonRequired != nil {
		settings.AppealReviewReasonRequired = *input.AppealReviewReasonRequired
	}
	if input.AuditMirrorChannelDiscordID != nil {
		value, err := normalizeChannelID(*input.AuditMirrorChannelDiscordID)
		if err != nil {
			return err
		}
		settings.AuditMirrorChannelDiscordID = value
	}
	if input.ManagedEvidenceChannelDiscordID != nil {
		return settingsValidationError("managed evidence channel is maintained by Quack")
	}
	if input.NotificationIntroduction != nil {
		value := strings.TrimSpace(*input.NotificationIntroduction)
		if len(value) > maxNotificationBrandingLength {
			return settingsValidationError(fmt.Sprintf("notification introduction exceeds %d characters", maxNotificationBrandingLength))
		}
		settings.NotificationIntroduction = value
	}
	if input.NotificationFooter != nil {
		value := strings.TrimSpace(*input.NotificationFooter)
		if len(value) > maxNotificationBrandingLength {
			return settingsValidationError(fmt.Sprintf("notification footer exceeds %d characters", maxNotificationBrandingLength))
		}
		settings.NotificationFooter = value
	}
	return nil
}

// applyModuleInput returns states with the input's module switches applied.
func applyModuleInput(states ModuleStates, input GuildSettingsInput) ModuleStates {
	if input.TicketsEnabled != nil {
		states.Tickets = *input.TicketsEnabled
	}
	if input.GeneralLoggingEnabled != nil {
		states.GeneralLogging = *input.GeneralLoggingEnabled
	}
	if input.HoneypotEnabled != nil {
		states.Honeypot = *input.HoneypotEnabled
	}
	return states
}

// normalizeChannelID accepts "" (clear) or a canonical decimal snowflake.
func normalizeChannelID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	if len(value) > 20 {
		return "", settingsValidationError("Discord channel reference exceeds 20 digits")
	}
	snowflake, err := strconv.ParseUint(value, 10, 64)
	if err != nil || snowflake == 0 || strconv.FormatUint(snowflake, 10) != value {
		return "", settingsValidationError("Discord channel reference must be a decimal snowflake")
	}
	return value, nil
}

// discordInviteCode is the shape of a Discord invite code.
var discordInviteCode = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// normalizeRejoinURL accepts "" (clear) or an https discord.gg or
// discord.com/invite link without credentials, query, or fragment, and
// returns its canonical https://discord.gg/<code> form.
func normalizeRejoinURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	invalid := settingsValidationError("use an HTTPS Discord invite link")
	parsed, err := url.Parse(value)
	if err != nil || len(value) > 256 || parsed.Scheme != "https" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalid
	}
	var code string
	switch strings.ToLower(parsed.Host) {
	case "discord.gg":
		code = strings.TrimPrefix(parsed.Path, "/")
	case "discord.com", "www.discord.com":
		var found bool
		code, found = strings.CutPrefix(parsed.Path, "/invite/")
		if !found {
			code = ""
		}
	}
	if !discordInviteCode.MatchString(code) {
		return "", invalid
	}
	return "https://discord.gg/" + code, nil
}

func (s *GuildSettingsService) audit(ctx context.Context, guildContext *GuildStaffContext, action string, result AuditResult, failureReason string) error {
	return recordStaffAudit(ctx, s.store, guildContext, action, "guild_settings", "", result, failureReason)
}

// settingsAudit is the success entry the store writes with a settings change.
func settingsAudit(ctx context.Context, guildContext *GuildStaffContext, action string) *AuditLogEntry {
	return staffAudit(ctx, guildContext, action, "guild_settings", "", AuditResultSuccess, "")
}

func guildSettingsResponse(settings GuildSettings, modules ModuleStates) GuildSettingsResponse {
	return GuildSettingsResponse{
		ID:                                settings.ID,
		GuildID:                           settings.GuildID,
		AppealQueueChannelDiscordID:       settings.AppealQueueChannelDiscordID,
		AppealRejoinURL:                   settings.AppealRejoinURL,
		AppealReviewReasonRequired:        settings.AppealReviewReasonRequired,
		AuditMirrorChannelDiscordID:       settings.AuditMirrorChannelDiscordID,
		ManagedEvidenceChannelDiscordID:   settings.ManagedEvidenceChannelDiscordID,
		NotificationIntroduction:          settings.NotificationIntroduction,
		NotificationFooter:                settings.NotificationFooter,
		TicketsEnabled:                    modules.Tickets,
		GeneralLoggingEnabled:             modules.GeneralLogging,
		HoneypotEnabled:                   modules.Honeypot,
		StarterPolicyTemplateID:           settings.StarterPolicyTemplateID,
		StarterPolicyReviewRequired:       settings.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: settings.StarterPolicyNoticeAcknowledgedAt,
	}
}
