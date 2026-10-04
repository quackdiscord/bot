package quack

import (
	"context"
	"errors"
	"strings"
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
	Live               DiscordGuildAuthorization
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

// applyLive refreshes the context's permissions from a new snapshot.
func (c *GuildStaffContext) applyLive(snapshot *DiscordGuildAuthorization, actorID string) {
	c.Live = *snapshot
	c.PermissionBits = snapshot.Actor.PermissionBits
	role := roleFromPermissions(snapshot.Actor.PermissionBits, snapshot.Guild.OwnerID == actorID)
	c.Permissions = role.permissions
	c.IsAdmin = role.isAdmin
	c.IsModerator = role.isModerator
}

// staffRole is what a member's Discord permissions allow them to do in
// Quack.
type staffRole struct {
	isAdmin     bool
	isModerator bool
	permissions map[PermissionAction]bool
}

// roleFromPermissions maps Discord permissions to Quack capabilities.
// Moderate Members can apply and read cases; Manage Guild can configure
// templates and settings; owners and administrators can do both.
func roleFromPermissions(bits uint64, isOwner bool) staffRole {
	isAdmin := isOwner || hasAllBits(bits, permissionAdministrator)
	hasModerate := hasAllBits(bits, permissionModerateMembers)
	canManage := isAdmin || hasAllBits(bits, permissionManageGuild)
	canModerate := isAdmin || hasModerate
	return staffRole{
		isAdmin:     isAdmin,
		isModerator: !isAdmin && hasModerate,
		permissions: map[PermissionAction]bool{
			PermissionActionCaseCreate:         canModerate,
			PermissionActionCaseRead:           canModerate,
			PermissionActionCaseTemplateRead:   canModerate || canManage,
			PermissionActionCaseTemplateWrite:  canManage,
			PermissionActionCaseTemplateDelete: canManage,
			PermissionActionAppealReview:       canModerate,
			PermissionActionTicketResolve:      canModerate,
			PermissionActionAuditRead:          canModerate,
			PermissionActionGuildSettingsRead:  canManage,
			PermissionActionGuildSettingsWrite: canManage,
			PermissionActionCaseVoid:           canModerate,
			PermissionActionFailureDismiss:     canModerate,
		},
	}
}

// PermissionMapStrings converts a capability map to string keys for JSON.
func PermissionMapStrings(permissions map[PermissionAction]bool) map[string]bool {
	out := make(map[string]bool, len(permissions))
	for action, allowed := range permissions {
		out[string(action)] = allowed
	}
	return out
}

// UserGuildListItem is a guild the signed-in user can manage or moderate.
type UserGuildListItem struct {
	DiscordGuildID  string `json:"discord_guild_id"`
	Name            string `json:"name"`
	IconURL         string `json:"icon_url"`
	PermissionBits  string `json:"permission_bits"`
	IsOwner         bool   `json:"is_owner"`
	IsAdministrator bool   `json:"is_administrator"`
	CanManageGuild  bool   `json:"can_manage_guild"`
	CanModerate     bool   `json:"can_moderate"`
	QuackInGuild    bool   `json:"quack_in_guild"`
	QuackGuildName  string `json:"quack_guild_name,omitempty"`
}

// ListUserManageableGuilds lists the session user's guilds where they can
// manage or moderate, and whether Quack is installed in each.
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

	out := make([]UserGuildListItem, 0, len(userGuilds))
	for _, guild := range userGuilds {
		isAdmin := hasAllBits(guild.Permissions, permissionAdministrator)
		canManage := guild.Owner || isAdmin || hasAllBits(guild.Permissions, permissionManageGuild)
		canModerate := guild.Owner || isAdmin || hasAllBits(guild.Permissions, permissionModerateMembers)
		if !canManage && !canModerate {
			continue
		}
		item := UserGuildListItem{
			DiscordGuildID:  guild.ID,
			Name:            guild.Name,
			IconURL:         discordGuildIconURL(guild.ID, guild.Icon),
			PermissionBits:  PermissionBitsString(guild.Permissions),
			IsOwner:         guild.Owner,
			IsAdministrator: isAdmin,
			CanManageGuild:  canManage,
			CanModerate:     canModerate,
		}
		if botGuild, ok := botGuildsByID[guild.ID]; ok {
			item.QuackInGuild = true
			item.QuackGuildName = botGuild.Name
		}
		out = append(out, item)
	}
	return out, nil
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
// are not trusted.
func (s *GuildService) ResolveDiscordStaffContext(ctx context.Context, input DiscordStaffContextInput) (*GuildStaffContext, error) {
	if s.discord != nil && strings.TrimSpace(input.DiscordGuildID) != "" && strings.TrimSpace(input.DiscordUserID) == "" {
		return nil, errors.New("missing discord user id")
	}
	return s.resolve(ctx, input.DiscordGuildID, strings.TrimSpace(input.DiscordUserID), input.DisplayName)
}

// resolve fetches the guild and actor from Discord, refreshes the cached
// guild and staff records, and derives the actor's capabilities.
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
	var staff *StaffMember
	if snapshot.Actor.Present {
		displayName := strings.TrimSpace(snapshot.Actor.DisplayName)
		if displayName == "" {
			displayName = strings.TrimSpace(fallbackDisplayName)
		}
		if displayName == "" {
			displayName = actorID
		}
		staff, err = s.store.UpsertStaffMember(ctx, UpsertStaffMemberParams{
			GuildID:                guild.ID,
			DiscordUserID:          actorID,
			LastSeenPermissionBits: snapshot.Actor.PermissionBits,
			LastKnownDisplayName:   displayName,
			LastActiveAt:           time.Now().UTC(),
		})
	} else {
		staff, err = s.store.GetStaffMember(ctx, guild.ID, actorID)
	}
	if err != nil {
		return nil, err
	}

	guildContext := &GuildStaffContext{Guild: guild, Staff: staff, ActorDiscordUserID: actorID}
	guildContext.applyLive(snapshot, actorID)
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
	status.ManagedChannels["evidence"] = strings.TrimSpace(settings.ManagedEvidenceChannelDiscordID) != ""
	status.ManagedChannels["audit_mirror"] = strings.TrimSpace(settings.AuditMirrorChannelDiscordID) != ""
	if !status.ManagedChannels["evidence"] {
		status.Degraded = true
		status.Reasons = append(status.Reasons, "managed_evidence_channel_unavailable")
	}
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
