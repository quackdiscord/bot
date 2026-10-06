package quack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// PermissionAction names a capability a staff member can hold in a guild.
// Capabilities are derived from live Discord permissions, never stored.
type PermissionAction string

// The capabilities checked by the services and HTTP middleware.
const (
	PermissionActionCaseCreate         PermissionAction = "case.create"
	PermissionActionCaseRead           PermissionAction = "case.read"
	PermissionActionCaseTemplateRead   PermissionAction = "case_template.read"
	PermissionActionCaseTemplateWrite  PermissionAction = "case_template.write"
	PermissionActionCaseTemplateDelete PermissionAction = "case_template.delete"
	PermissionActionAppealReview       PermissionAction = "appeal.review"
	PermissionActionTicketResolve      PermissionAction = "ticket.resolve"
	PermissionActionAuditRead          PermissionAction = "audit.read"
	PermissionActionGuildSettingsRead  PermissionAction = "guild_settings.read"
	PermissionActionGuildSettingsWrite PermissionAction = "guild_settings.write"
	PermissionActionCaseVoid           PermissionAction = "case.void"
	PermissionActionFailureDismiss     PermissionAction = "action_failure.dismiss"
	// PermissionActionStaffRolesWrite changes the moderator roles. Only the
	// owner and Administrators hold it: a moderator role lets Quack time
	// out, kick, and ban for its holders, which is more than Manage Guild
	// can grant in Discord.
	PermissionActionStaffRolesWrite PermissionAction = "staff_roles.write"
)

// OAuthState is the server-side half of a Discord OAuth login in progress.
type OAuthState struct {
	RedirectTo   string    `json:"redirect_to"`
	ResponseMode string    `json:"response_mode"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuthSession is a signed-in dashboard user. Tokens never leave the server.
type AuthSession struct {
	ID               string    `json:"-"`
	DiscordUserID    string    `json:"discord_user_id"`
	Username         string    `json:"username"`
	GlobalName       string    `json:"global_name"`
	Avatar           string    `json:"avatar"`
	AccessToken      string    `json:"-"`
	RefreshToken     string    `json:"-"`
	CSRFToken        string    `json:"-"`
	TokenType        string    `json:"token_type"`
	Scope            string    `json:"scope"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
}

// GuildService turns Discord identities into authorized staff contexts and
// handles the guild lifecycle: install, departure, and channel cleanup.
type GuildService struct {
	store   GuildStore
	discord GuildDirectory
}

// NewGuildService returns a GuildService. discord may be nil, in which case
// every live authorization fails with ErrAuthorizationUnavailable.
func NewGuildService(store GuildStore, discord GuildDirectory) *GuildService {
	return &GuildService{store: store, discord: discord}
}

// GuildStaffContext is a staff member acting in a guild, resolved from live
// Discord state at the start of a request. Resolve it once per request and
// pass it down; services refresh it in place when they re-check Discord.
type GuildStaffContext struct {
	Guild              *Guild
	Staff              *StaffMember
	ActorDiscordUserID string
	PermissionBits     uint64
	Permissions        map[PermissionAction]bool
	IsAdmin            bool
	IsModerator        bool
	// MFARequired means the actor would be staff, but the guild requires
	// two-factor authentication that Quack has not confirmed for them, so
	// every capability is withheld until they turn it on and sign in to
	// the dashboard.
	MFARequired bool
	// StaffRoles are the guild's configured staff roles, read with the
	// context so later Discord re-checks apply the same configuration.
	StaffRoles StaffRoles
	// ActorMFAEnabled is whether Quack has confirmed the actor's 2FA. It is
	// only read when the guild requires 2FA.
	ActorMFAEnabled bool
	Live            DiscordGuildAuthorization
}

// isStaff reports whether the context grants any capability.
func (c *GuildStaffContext) isStaff() bool {
	for _, allowed := range c.Permissions {
		if allowed {
			return true
		}
	}
	return false
}

// Can reports whether the context grants action.
func (c *GuildStaffContext) Can(action PermissionAction) bool {
	if c == nil {
		return false
	}
	return c.Permissions[action]
}

// actorID returns the acting Discord user, falling back to the staff record
// for contexts built by hand rather than resolved from Discord.
func (c *GuildStaffContext) actorID() string {
	if c.ActorDiscordUserID == "" && c.Staff != nil {
		return c.Staff.DiscordUserID
	}
	return c.ActorDiscordUserID
}

// applyLive refreshes the context's capabilities from a new snapshot, with
// the staff roles and 2FA status already on the context.
func (c *GuildStaffContext) applyLive(snapshot *DiscordGuildAuthorization, actorID string) {
	c.Live = *snapshot
	c.PermissionBits = snapshot.Actor.PermissionBits
	access := deriveStaffAccess(staffAccessInput{
		permissionBits:   snapshot.Actor.PermissionBits,
		isOwner:          snapshot.Guild.OwnerID == actorID,
		roleIDs:          snapshot.Actor.RoleIDs,
		roles:            c.StaffRoles,
		guildRoleIDs:     snapshot.Guild.RoleIDs,
		guildRequiresMFA: snapshot.Guild.MFARequired,
		actorMFAEnabled:  c.ActorMFAEnabled,
	})
	c.Permissions = access.permissions
	c.IsAdmin = access.isAdmin
	c.IsModerator = access.isModerator
	c.MFARequired = access.mfaRequired
}

// loadStaffAccess reads what capabilities depend on besides the Discord
// snapshot: the guild's configured staff roles and, when the guild requires
// 2FA, whether Quack has confirmed the actor's.
func (s *GuildService) loadStaffAccess(ctx context.Context, guildContext *GuildStaffContext, snapshot *DiscordGuildAuthorization) error {
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		return err
	}
	guildContext.StaffRoles = StaffRoles{}
	if settings != nil {
		guildContext.StaffRoles = settings.StaffRoles()
	}
	guildContext.ActorMFAEnabled = false
	if actorID := guildContext.actorID(); snapshot.Guild.MFARequired && actorID != "" {
		enabled, err := s.store.DiscordUserMFAEnabled(ctx, actorID)
		if err != nil {
			return err
		}
		guildContext.ActorMFAEnabled = enabled
	}
	return nil
}

// GuildStaffRoles returns the staff roles configured for a guild, or none
// for a guild Quack has no settings for. Discord adapters use it for staff
// channels, ticket threads, and the honeypot exemption.
func (s *GuildService) GuildStaffRoles(ctx context.Context, discordGuildID string) (StaffRoles, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return StaffRoles{}, err
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return StaffRoles{}, err
	}
	return settings.StaffRoles(), nil
}

// PermissionMapStrings converts a capability map to string keys for JSON.
func PermissionMapStrings(permissions map[PermissionAction]bool) map[string]bool {
	out := make(map[string]bool, len(permissions))
	for action, allowed := range permissions {
		out[string(action)] = allowed
	}
	return out
}

// UserGuildListItem is a guild where the signed-in user is staff, or would
// be once they confirm 2FA.
type UserGuildListItem struct {
	DiscordGuildID  string `json:"discord_guild_id"`
	Name            string `json:"name"`
	IconURL         string `json:"icon_url"`
	PermissionBits  string `json:"permission_bits"`
	IsOwner         bool   `json:"is_owner"`
	IsAdministrator bool   `json:"is_administrator"`
	CanManageGuild  bool   `json:"can_manage_guild"`
	CanModerate     bool   `json:"can_moderate"`
	// CanManageRules is template management, from Manage Guild or a
	// configured rules manager role.
	CanManageRules bool `json:"can_manage_rules"`
	// MFARequired means the guild requires 2FA that Quack has not confirmed
	// for the user; every capability above is then false.
	MFARequired    bool   `json:"mfa_required"`
	QuackInGuild   bool   `json:"quack_in_guild"`
	QuackGuildName string `json:"quack_guild_name,omitempty"`
}

// userGuildRoleLookups bounds how many guilds ListUserManageableGuilds
// reads the user's roles in at once.
const userGuildRoleLookups = 4

// ListUserManageableGuilds lists the session user's guilds where they are
// staff, and whether Quack is installed in each.
//
// Discord's guild list carries permission bits but no roles, so the user's
// roles are read from the bot's member state only where they can change the
// answer: guilds with Quack installed, where the user is not the owner or an
// administrator, that configured moderator roles (or rules manager roles,
// for users without Manage Guild). Those guilds' staff roles come from one
// store query and the lookups run a few at a time.
func (s *GuildService) ListUserManageableGuilds(ctx context.Context, session *AuthSession) ([]UserGuildListItem, error) {
	if s.discord == nil {
		return nil, errors.New("guild service is not configured")
	}
	if session == nil || session.AccessToken == "" {
		return nil, errors.New("missing auth session")
	}
	userGuilds, err := s.discord.UserGuilds(ctx, session.AccessToken)
	if err != nil {
		return nil, err
	}
	botGuilds, err := s.discord.BotGuilds(ctx)
	if err != nil {
		return nil, err
	}
	botGuildsByID := make(map[string]DiscordBotGuild, len(botGuilds))
	for _, guild := range botGuilds {
		botGuildsByID[guild.ID] = guild
	}

	// Only installed guilds the bits do not settle need their staff roles,
	// and only guilds requiring 2FA need the user's 2FA status.
	var unsettled []string
	requiresMFA := false
	for _, guild := range userGuilds {
		botGuild, installed := botGuildsByID[guild.ID]
		if !installed {
			continue
		}
		requiresMFA = requiresMFA || botGuild.MFARequired
		if !guild.Owner && !hasAllBits(guild.Permissions, permissionAdministrator) {
			unsettled = append(unsettled, guild.ID)
		}
	}
	staffRoles := map[string]StaffRoles{}
	if len(unsettled) > 0 {
		if staffRoles, err = s.store.ListGuildStaffRoles(ctx, unsettled); err != nil {
			return nil, err
		}
	}
	mfaEnabled := false
	if requiresMFA {
		if mfaEnabled, err = s.store.DiscordUserMFAEnabled(ctx, session.DiscordUserID); err != nil {
			return nil, err
		}
	}
	lookups := s.userGuildRoles(ctx, session.DiscordUserID, userGuilds, staffRoles)

	out := make([]UserGuildListItem, 0, len(userGuilds))
	for _, guild := range userGuilds {
		botGuild, installed := botGuildsByID[guild.ID]
		roles := staffRoles[guild.ID]
		lookup, looked := lookups[guild.ID]
		if looked && lookup.failed {
			// Without the member's roles only their permission bits can
			// answer, so the guild shows as the bits alone would have it.
			roles = StaffRoles{}
		}
		access := deriveStaffAccess(staffAccessInput{
			permissionBits:   guild.Permissions,
			isOwner:          guild.Owner,
			roleIDs:          lookup.roleIDs,
			roles:            roles,
			guildRoleIDs:     lookup.guildRoleIDs,
			guildRequiresMFA: installed && botGuild.MFARequired,
			actorMFAEnabled:  mfaEnabled,
		})
		if !access.canManageGuild && !access.canModerate && !access.canManageRules && !access.mfaRequired {
			continue
		}
		item := UserGuildListItem{
			DiscordGuildID:  guild.ID,
			Name:            guild.Name,
			IconURL:         discordGuildIconURL(guild.ID, guild.Icon),
			PermissionBits:  PermissionBitsString(guild.Permissions),
			IsOwner:         guild.Owner,
			IsAdministrator: hasAllBits(guild.Permissions, permissionAdministrator),
			CanManageGuild:  access.canManageGuild,
			CanModerate:     access.canModerate,
			CanManageRules:  access.canManageRules,
			MFARequired:     access.mfaRequired,
		}
		if installed {
			item.QuackInGuild = true
			item.QuackGuildName = botGuild.Name
		}
		out = append(out, item)
	}
	return out, nil
}

// userGuildRoleLookup is what ListUserManageableGuilds learned about the
// user's roles in one guild.
type userGuildRoleLookup struct {
	roleIDs, guildRoleIDs []string
	// failed means Discord could not say, so only permission bits count.
	failed bool
}

// userGuildRoles reads the user's current roles, and the guild's, in each
// guild whose configured staff roles could change their access,
// userGuildRoleLookups at a time. A failed lookup is logged and marked
// failed.
func (s *GuildService) userGuildRoles(ctx context.Context, userID string, guilds []DiscordUserGuild, staffRoles map[string]StaffRoles) map[string]userGuildRoleLookup {
	var lookups []string
	for _, guild := range guilds {
		roles, ok := staffRoles[guild.ID]
		if !ok {
			continue
		}
		manages := hasAllBits(guild.Permissions, permissionManageGuild)
		if len(roles.ModeratorRoleIDs) > 0 || (len(roles.RulesManagerRoleIDs) > 0 && !manages) {
			lookups = append(lookups, guild.ID)
		}
	}
	found := make([]userGuildRoleLookup, len(lookups))
	limit := make(chan struct{}, userGuildRoleLookups)
	var wg sync.WaitGroup
	for i, guildID := range lookups {
		wg.Go(func() {
			limit <- struct{}{}
			defer func() { <-limit }()
			snapshot, err := s.discord.GuildAuthorization(ctx, guildID, userID, "")
			switch {
			case err != nil || snapshot == nil || snapshot.Actor.DiscordUserID != userID:
				slog.WarnContext(ctx, "Server list role lookup failed; using permissions only",
					"discord_guild_id", guildID, "error", err)
				found[i] = userGuildRoleLookup{failed: true}
			case snapshot.Actor.Present:
				found[i] = userGuildRoleLookup{roleIDs: snapshot.Actor.RoleIDs, guildRoleIDs: snapshot.Guild.RoleIDs}
			default:
				found[i] = userGuildRoleLookup{guildRoleIDs: snapshot.Guild.RoleIDs}
			}
		})
	}
	wg.Wait()
	out := make(map[string]userGuildRoleLookup, len(lookups))
	for i, guildID := range lookups {
		out[guildID] = found[i]
	}
	return out
}

// ResolveStaffContext builds the staff context for a dashboard session in a
// guild from live Discord state.
func (s *GuildService) ResolveStaffContext(ctx context.Context, session *AuthSession, discordGuildID string) (*GuildStaffContext, error) {
	if session == nil || session.DiscordUserID == "" {
		return nil, errors.New("missing auth session")
	}
	return s.resolve(ctx, discordGuildID, session.DiscordUserID, sessionDisplayName(session))
}

// DiscordStaffContextInput identifies the member behind a Discord
// interaction.
type DiscordStaffContextInput struct {
	DiscordGuildID string
	DiscordUserID  string
	DisplayName    string
	PermissionBits uint64
	LastActiveAt   time.Time
}

// ResolveDiscordStaffContext builds the staff context for a Discord
// interaction from live Discord state. The interaction's own permission bits
// are not trusted. A member the guild's 2FA requirement blocks gets a context with
// MFARequired set and no capabilities; Authorize refuses it.
func (s *GuildService) ResolveDiscordStaffContext(ctx context.Context, input DiscordStaffContextInput) (*GuildStaffContext, error) {
	if s.discord != nil && strings.TrimSpace(input.DiscordGuildID) != "" && strings.TrimSpace(input.DiscordUserID) == "" {
		return nil, errors.New("missing discord user id")
	}
	return s.resolve(ctx, input.DiscordGuildID, strings.TrimSpace(input.DiscordUserID), input.DisplayName)
}

// resolve fetches the guild and actor from Discord, refreshes the cached
// guild and staff records, reads the guild's staff roles and the actor's
// 2FA status, and derives the actor's capabilities.
func (s *GuildService) resolve(ctx context.Context, discordGuildID, actorID, fallbackDisplayName string) (*GuildStaffContext, error) {
	if s.discord == nil {
		return nil, errors.New("discord client is not configured")
	}
	discordGuildID = strings.TrimSpace(discordGuildID)
	if discordGuildID == "" {
		return nil, errors.New("missing discord guild id")
	}
	snapshot, err := s.discord.GuildAuthorization(ctx, discordGuildID, actorID, "")
	if errors.Is(err, ErrBotNotInGuild) {
		return nil, ErrBotNotInGuild
	}
	if err != nil || snapshot == nil || snapshot.Guild.ID != discordGuildID || snapshot.Actor.DiscordUserID != actorID {
		return nil, ErrAuthorizationUnavailable
	}

	guild, err := s.store.UpsertGuild(ctx, UpsertGuildParams{
		DiscordGuildID:     snapshot.Guild.ID,
		Name:               snapshot.Guild.Name,
		IconURL:            discordGuildIconURL(snapshot.Guild.ID, snapshot.Guild.Icon),
		OwnerDiscordUserID: snapshot.Guild.OwnerID,
	})
	if err != nil {
		return nil, err
	}
	guildContext := &GuildStaffContext{Guild: guild, ActorDiscordUserID: actorID}
	if err := s.loadStaffAccess(ctx, guildContext, snapshot); err != nil {
		return nil, err
	}
	guildContext.applyLive(snapshot, actorID)

	// Only staff get a staff record written. Members who are not staff,
	// including those whose staff role the 2FA requirement withholds, keep
	// any record they already have; a present member without one gets an
	// unsaved record, so denials still name them.
	displayName := strings.TrimSpace(snapshot.Actor.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(fallbackDisplayName)
	}
	if displayName == "" {
		displayName = actorID
	}
	var staff *StaffMember
	if snapshot.Actor.Present && guildContext.isStaff() {
		staff, err = s.store.UpsertStaffMember(ctx, UpsertStaffMemberParams{
			GuildID:                guild.ID,
			DiscordUserID:          actorID,
			LastSeenPermissionBits: snapshot.Actor.PermissionBits,
			LastKnownDisplayName:   displayName,
			LastActiveAt:           time.Now().UTC(),
		})
	} else {
		staff, err = s.store.GetStaffMember(ctx, guild.ID, actorID)
		if err == nil && staff == nil && snapshot.Actor.Present {
			staff = &StaffMember{
				GuildID:                guild.ID,
				DiscordUserID:          actorID,
				LastSeenPermissionBits: snapshot.Actor.PermissionBits,
				LastKnownDisplayName:   displayName,
			}
		}
	}
	if err != nil {
		return nil, err
	}
	guildContext.Staff = staff
	return guildContext, nil
}

// sessionDisplayName is the name a dashboard user shows in Discord.
func sessionDisplayName(session *AuthSession) string {
	if strings.TrimSpace(session.GlobalName) != "" {
		return session.GlobalName
	}
	return session.Username
}

// GuildOperationalHealth reports problems that affect one guild only, such
// as missing bot permissions or a missing evidence channel.
type GuildOperationalHealth struct {
	Degraded        bool            `json:"degraded"`
	Reasons         []string        `json:"reasons"`
	BotPermissions  map[string]bool `json:"bot_permissions"`
	ManagedChannels map[string]bool `json:"managed_channels"`
}

// OperationalGuildHealth checks the bot's live permissions and the guild's
// managed channels. Deleted channels are cleared when Discord reports them,
// so an empty reference means the channel is gone.
func (s *GuildService) OperationalGuildHealth(ctx context.Context, discordGuildID string) (GuildOperationalHealth, error) {
	status := GuildOperationalHealth{
		Reasons:         []string{},
		BotPermissions:  map[string]bool{},
		ManagedChannels: map[string]bool{},
	}
	if s.discord == nil {
		return status, ErrAuthorizationUnavailable
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return status, ErrBotNotInGuild
	}
	live, err := s.discord.GuildAuthorization(ctx, guild.DiscordGuildID, "", "")
	if err != nil || live == nil || !live.Bot.Present {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "discord_bot_unavailable")
		return status, nil
	}
	required := map[string]uint64{
		"moderate_members": permissionModerateMembers,
		"kick_members":     permissionKickMembers,
		"ban_members":      permissionBanMembers,
		"manage_channels":  permissionManageChannels,
	}
	for name, permission := range required {
		available := hasDiscordPermission(live.Bot.PermissionBits, permission)
		status.BotPermissions[name] = available
		if !available {
			status.Degraded = true
			status.Reasons = append(status.Reasons, "missing_bot_permission:"+name)
		}
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return status, err
	}
	// Both channels are optional, so an unset one is not degraded.
	status.ManagedChannels["evidence"] = strings.TrimSpace(settings.ManagedEvidenceChannelDiscordID) != ""
	status.ManagedChannels["audit_mirror"] = strings.TrimSpace(settings.AuditMirrorChannelDiscordID) != ""
	return status, nil
}

// DiscordGuildLifecycleInput is guild metadata from a Discord gateway event.
type DiscordGuildLifecycleInput struct {
	DiscordGuildID     string
	Name               string
	Icon               string
	OwnerDiscordUserID string
	// KnownChannelDiscordIDs, when non-nil, is the guild's full channel
	// list, used to clear references to channels deleted while Quack was
	// away.
	KnownChannelDiscordIDs []string
}

// BootstrapDiscordGuild installs or reactivates a guild. It is idempotent:
// the starter template is created only for a guild that never had one.
func (s *GuildService) BootstrapDiscordGuild(ctx context.Context, input DiscordGuildLifecycleInput) (*BootstrapGuildResult, error) {
	guildID := strings.TrimSpace(input.DiscordGuildID)
	if guildID == "" || strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.OwnerDiscordUserID) == "" {
		return nil, errors.New("guild id, name, and owner are required")
	}
	return s.store.BootstrapGuild(ctx, BootstrapGuildParams{
		DiscordGuildID:         guildID,
		Name:                   strings.TrimSpace(input.Name),
		IconURL:                discordGuildIconURL(guildID, strings.TrimSpace(input.Icon)),
		OwnerDiscordUserID:     strings.TrimSpace(input.OwnerDiscordUserID),
		KnownChannelDiscordIDs: input.KnownChannelDiscordIDs,
		Starter:                StarterTemplate(),
	})
}

// DeactivateDiscordGuild marks a guild Quack left as inactive. Its history
// and settings are kept in case Quack is added back.
func (s *GuildService) DeactivateDiscordGuild(ctx context.Context, discordGuildID string) (*Guild, error) {
	audit := systemGuildAudit("guild.lifecycle.leave", "guild")
	return s.store.DeactivateGuild(ctx, strings.TrimSpace(discordGuildID), audit)
}

// ClearDeletedChannel removes settings references to a channel Discord
// reported deleted.
func (s *GuildService) ClearDeletedChannel(ctx context.Context, discordGuildID, channelID string) (*GuildSettings, error) {
	guild, err := s.store.GetGuildByDiscordID(ctx, strings.TrimSpace(discordGuildID))
	if err != nil || guild == nil {
		return nil, err
	}
	audit := systemGuildAudit("guild_settings.channel_reference.cleared", "guild_settings")
	return s.store.ClearGuildChannelReferences(ctx, guild.ID, strings.TrimSpace(channelID), audit)
}

// systemGuildAudit attributes a lifecycle change to Quack, which acts on
// Discord's events rather than on behalf of a staff member.
func systemGuildAudit(action, resourceType string) *AuditLogEntry {
	return &AuditLogEntry{
		ActorDiscordUserID: systemActorID,
		Source:             AuditSourceDiscord,
		Action:             action,
		ResourceType:       resourceType,
		Result:             AuditResultSuccess,
		MetadataJSON:       "{}",
	}
}
