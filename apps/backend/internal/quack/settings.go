package quack

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// maxNotificationBrandingLength bounds the guild text added to case
// notifications. Rendering truncates further; this only rejects abuse.
const maxNotificationBrandingLength = 2000

// maxStaffRoles bounds each configured staff role list.
const maxStaffRoles = 25

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

// ModuleEnablementChecker checks a module's saved setup against live
// Discord before the settings service switches it on, so a module is never
// on with channels or permissions it cannot use. A ModuleToggles may
// implement it; modules.Registry does.
type ModuleEnablementChecker interface {
	// CheckModuleEnablement returns why the modules set in on cannot be
	// switched on in guild, or nil.
	CheckModuleEnablement(ctx context.Context, guild *Guild, on ModuleStates) error
}

// GuildSettingsService reads and updates a guild's core settings. All access
// needs Manage Guild; changes and failed changes are audited.
type GuildSettingsService struct {
	store    SettingsStore
	channels StaffChannelValidator
	modules  ModuleToggles
}

// NewGuildSettingsService returns a GuildSettingsService. Without channels,
// setting an audit channel fails validation, and so does setting staff
// roles unless channels also implements GuildRoleReader. Without modules,
// every module reads as off and switching one on fails validation.
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
	// ManagedEvidenceChannelDiscordID is the staff-only channel Quack copies
	// evidence files into; empty turns copying off.
	ManagedEvidenceChannelDiscordID *string `json:"managed_evidence_channel_discord_id"`
	NotificationIntroduction        *string `json:"notification_introduction"`
	NotificationFooter              *string `json:"notification_footer"`
	// ModeratorRoleIDs replaces the moderator roles. Each must be a role in
	// the guild other than @everyone; duplicates are dropped, and at most
	// 25 remain. Empty makes Moderate Members the moderator permission.
	ModeratorRoleIDs *[]string `json:"moderator_role_ids"`
	// RulesManagerRoleIDs replaces the rules manager roles, under the same
	// rules as ModeratorRoleIDs.
	RulesManagerRoleIDs *[]string `json:"rules_manager_role_ids"`
	// The module switches are stored with the modules, not in guild_settings.
	TicketsEnabled        *bool `json:"tickets_enabled"`
	GeneralLoggingEnabled *bool `json:"general_logging_enabled"`
	HoneypotEnabled       *bool `json:"honeypot_enabled"`
}

// GuildSettingsResponse is a guild's settings as the dashboard sees them.
type GuildSettingsResponse struct {
	ID                              string `json:"id"`
	GuildID                         string `json:"guild_id"`
	AppealQueueChannelDiscordID     string `json:"appeal_queue_channel_discord_id,omitempty"`
	AppealRejoinURL                 string `json:"appeal_rejoin_url,omitempty"`
	AppealReviewReasonRequired      bool   `json:"appeal_review_reason_required"`
	AuditMirrorChannelDiscordID     string `json:"audit_mirror_channel_discord_id,omitempty"`
	ManagedEvidenceChannelDiscordID string `json:"managed_evidence_channel_discord_id,omitempty"`
	NotificationIntroduction        string `json:"notification_introduction,omitempty"`
	NotificationFooter              string `json:"notification_footer,omitempty"`
	// ModeratorRoleIDs are the roles that make members moderators; empty
	// means Moderate Members does.
	ModeratorRoleIDs []string `json:"moderator_role_ids" nullable:"false" required:"true"`
	// RulesManagerRoleIDs are the roles that let members manage templates.
	RulesManagerRoleIDs               []string   `json:"rules_manager_role_ids" nullable:"false" required:"true"`
	TicketsEnabled                    bool       `json:"tickets_enabled"`
	GeneralLoggingEnabled             bool       `json:"general_logging_enabled"`
	HoneypotEnabled                   bool       `json:"honeypot_enabled"`
	StarterPolicyTemplateID           string     `json:"starter_policy_template_id"`
	StarterPolicyReviewRequired       bool       `json:"starter_policy_review_required"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `json:"starter_policy_notice_acknowledged_at,omitempty"`
}

// Get returns the guild's settings. Reads are not audited.
func (s *GuildSettingsService) Get(ctx context.Context, guildContext *GuildStaffContext) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil {
		return nil, errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsRead) {
		return nil, ErrGuildSettingsPermissionDenied
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return nil, ErrGuildSettingsNotFound
	}
	states, err := s.moduleStates(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	response := guildSettingsResponse(*settings, states)
	return &response, nil
}

// Update applies a partial settings update. A new audit or appeal queue
// channel must pass StaffChannelValidator, judged with the staff roles the
// update leaves. Changing the moderator roles also needs
// PermissionActionStaffRolesWrite.
func (s *GuildSettingsService) Update(ctx context.Context, guildContext *GuildStaffContext, input GuildSettingsInput) (*GuildSettingsResponse, error) {
	ctx = ensureTraceContext(ctx)
	const action = string(AuditActionSettingsUpdate)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, errNoGuildContext
	}
	if !guildContext.Can(PermissionActionGuildSettingsWrite) {
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
	stored := settings.StaffRoles()
	if err := applyGuildSettingsInput(settings, input, guildContext.Guild.DiscordGuildID); err != nil {
		_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		return nil, err
	}
	if err := s.settleStaffRoles(ctx, guildContext, stored, settings, input); err != nil {
		if !errors.Is(err, ErrGuildSettingsPermissionDenied) {
			_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
		}
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
	if on := (ModuleStates{
		Tickets:        wantStates.Tickets && !states.Tickets,
		GeneralLogging: wantStates.GeneralLogging && !states.GeneralLogging,
		Honeypot:       wantStates.Honeypot && !states.Honeypot,
	}); on != (ModuleStates{}) {
		if checker, ok := s.modules.(ModuleEnablementChecker); ok {
			if err := checker.CheckModuleEnablement(ctx, guildContext.Guild, on); err != nil {
				err = settingsValidationError(err.Error())
				_ = s.audit(ctx, guildContext, action, AuditResultFailure, err.Error())
				return nil, err
			}
		}
	}
	staffChannels := []struct {
		changed   bool
		channelID string
		problem   string
	}{
		{input.AuditMirrorChannelDiscordID != nil, settings.AuditMirrorChannelDiscordID, "audit channel must be private and belong to this guild"},
		{input.AppealQueueChannelDiscordID != nil, settings.AppealQueueChannelDiscordID, "appeal queue channel must be private and belong to this guild"},
		{input.ManagedEvidenceChannelDiscordID != nil, settings.ManagedEvidenceChannelDiscordID, "evidence channel must be private and belong to this guild"},
	}
	for _, channel := range staffChannels {
		if !channel.changed || channel.channelID == "" {
			continue
		}
		if s.channels == nil {
			return nil, settingsValidationError("channel validation unavailable")
		}
		if err := s.validateStaffChannel(ctx, guildContext.Guild.DiscordGuildID, channel.channelID, *settings); err != nil {
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

// validateStaffChannel checks a staff channel against the staff roles
// settings will have once saved, when the validator can take them.
func (s *GuildSettingsService) validateStaffChannel(ctx context.Context, discordGuildID, channelID string, settings GuildSettings) error {
	if validator, ok := s.channels.(StaffRoleChannelValidator); ok {
		return validator.ValidateStaffChannelForRoles(ctx, discordGuildID, channelID, settings.StaffRoles())
	}
	return s.channels.ValidateStaffChannel(ctx, discordGuildID, channelID)
}

// errStaffRolesDenied refuses a moderator role change by someone without
// PermissionActionStaffRolesWrite.
var errStaffRolesDenied = fmt.Errorf("%w: only the server owner or Administrators can change moderator roles",
	ErrGuildSettingsPermissionDenied)

// settleStaffRoles checks and finishes the staff role lists input sets,
// already normalized onto settings, against stored, the lists before this
// update. Changing the moderator roles needs PermissionActionStaffRolesWrite;
// sending the same roles again does not. Roles not stored before must be
// current roles of the guild. Stored roles since deleted in Discord are
// dropped rather than refused, and dropping one is not a change. A refusal
// for permission wraps ErrGuildSettingsPermissionDenied.
func (s *GuildSettingsService) settleStaffRoles(ctx context.Context, guildContext *GuildStaffContext, stored StaffRoles, settings *GuildSettings, input GuildSettingsInput) error {
	if input.ModeratorRoleIDs == nil && input.RulesManagerRoleIDs == nil {
		return nil
	}
	// existing is nil when the guild's roles cannot be read at all.
	var existing map[string]bool
	if reader, ok := s.channels.(GuildRoleReader); ok {
		roles, err := reader.GuildRoles(ctx, guildContext.Guild.DiscordGuildID)
		if err != nil {
			return settingsValidationError("could not read the server's roles; try again")
		}
		existing = make(map[string]bool, len(roles))
		for _, role := range roles {
			existing[role.ID] = true
		}
	}
	lists := []struct {
		changed        bool
		stored         []string
		requested      *[]string
		needsStaffRole bool
	}{
		{input.ModeratorRoleIDs != nil, stored.ModeratorRoleIDs, &settings.ModeratorRoleIDs, true},
		{input.RulesManagerRoleIDs != nil, stored.RulesManagerRoleIDs, &settings.RulesManagerRoleIDs, false},
	}
	for _, list := range lists {
		if !list.changed {
			continue
		}
		// Stored roles that are gone from Discord leave both sides.
		kept := slices.DeleteFunc(slices.Clone(*list.requested), func(roleID string) bool {
			return existing != nil && !existing[roleID] && slices.Contains(list.stored, roleID)
		})
		before := slices.DeleteFunc(slices.Clone(list.stored), func(roleID string) bool {
			return existing != nil && !existing[roleID]
		})
		if list.needsStaffRole && !sameRoleSet(kept, before) && !guildContext.Can(PermissionActionStaffRolesWrite) {
			return errStaffRolesDenied
		}
		for _, roleID := range kept {
			if slices.Contains(list.stored, roleID) {
				continue
			}
			if existing == nil {
				return settingsValidationError("role validation unavailable")
			}
			if !existing[roleID] {
				return settingsValidationError("staff roles must be roles in this server")
			}
		}
		*list.requested = kept
	}
	return nil
}

// sameRoleSet reports whether a and b hold the same role IDs in any order.
// Both are already free of duplicates.
func sameRoleSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, roleID := range a {
		if !slices.Contains(b, roleID) {
			return false
		}
	}
	return true
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

// applyGuildSettingsInput validates input's values that need no Discord
// lookup and copies them onto the settings of the guild discordGuildID.
func applyGuildSettingsInput(settings *GuildSettings, input GuildSettingsInput, discordGuildID string) error {
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
		value, err := normalizeChannelID(*input.ManagedEvidenceChannelDiscordID)
		if err != nil {
			return err
		}
		settings.ManagedEvidenceChannelDiscordID = value
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
	if input.ModeratorRoleIDs != nil {
		value, err := normalizeStaffRoleIDs(*input.ModeratorRoleIDs, discordGuildID)
		if err != nil {
			return err
		}
		settings.ModeratorRoleIDs = value
	}
	if input.RulesManagerRoleIDs != nil {
		value, err := normalizeStaffRoleIDs(*input.RulesManagerRoleIDs, discordGuildID)
		if err != nil {
			return err
		}
		settings.RulesManagerRoleIDs = value
	}
	return nil
}

// normalizeStaffRoleIDs checks a staff role list's shape: decimal
// snowflakes other than the guild's own ID (@everyone), at most
// maxStaffRoles after dropping duplicates. It returns the list in its
// original order.
func normalizeStaffRoleIDs(raw []string, discordGuildID string) ([]string, error) {
	out := make([]string, 0, len(raw))
	for _, entry := range raw {
		value := strings.TrimSpace(entry)
		snowflake, err := strconv.ParseUint(value, 10, 64)
		if err != nil || snowflake == 0 || strconv.FormatUint(snowflake, 10) != value {
			return nil, settingsValidationError("staff roles must be Discord role IDs")
		}
		if value == discordGuildID {
			return nil, settingsValidationError("@everyone cannot be a staff role")
		}
		if !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	if len(out) > maxStaffRoles {
		return nil, settingsValidationError(fmt.Sprintf("choose at most %d roles for each staff level", maxStaffRoles))
	}
	return out, nil
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
		ModeratorRoleIDs:                  nonNilStrings(settings.ModeratorRoleIDs),
		RulesManagerRoleIDs:               nonNilStrings(settings.RulesManagerRoleIDs),
		TicketsEnabled:                    modules.Tickets,
		GeneralLoggingEnabled:             modules.GeneralLogging,
		HoneypotEnabled:                   modules.Honeypot,
		StarterPolicyTemplateID:           settings.StarterPolicyTemplateID,
		StarterPolicyReviewRequired:       settings.StarterPolicyNoticePending,
		StarterPolicyNoticeAcknowledgedAt: settings.StarterPolicyNoticeAcknowledgedAt,
	}
}

// nonNilStrings returns values, or an empty slice for nil, so lists encode
// as [].
func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
