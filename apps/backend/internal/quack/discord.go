package quack

import (
	"context"
	"fmt"
	"strings"
)

// Discord permission bits Quack checks. They mirror Discord's documented
// values so the core does not depend on a Discord client library.
const (
	permissionKickMembers     uint64 = 1 << 1
	permissionBanMembers      uint64 = 1 << 2
	permissionAdministrator   uint64 = 1 << 3
	permissionManageChannels  uint64 = 1 << 4
	permissionManageGuild     uint64 = 1 << 5
	permissionModerateMembers uint64 = 1 << 40
)

// GuildDirectory reads guild membership and live permissions from Discord.
// Every authorization decision starts from a fresh GuildAuthorization.
type GuildDirectory interface {
	// UserGuilds lists the guilds of the user who owns accessToken.
	UserGuilds(ctx context.Context, accessToken string) ([]DiscordUserGuild, error)
	// BotGuilds lists the guilds the bot is in.
	BotGuilds(ctx context.Context) ([]DiscordBotGuild, error)
	// GuildAuthorization fetches the guild, the bot, and optionally an actor
	// and a target. Empty IDs are skipped.
	GuildAuthorization(ctx context.Context, discordGuildID, actorDiscordUserID, targetDiscordUserID string) (*DiscordGuildAuthorization, error)
}

// Enforcer performs moderation actions in Discord. reason is written to the
// guild's Discord audit log.
type Enforcer interface {
	TimeoutMember(ctx context.Context, discordGuildID, discordUserID string, durationSeconds int, reason string) (map[string]any, error)
	KickMember(ctx context.Context, discordGuildID, discordUserID, reason string) (map[string]any, error)
	BanMember(ctx context.Context, discordGuildID, discordUserID string, deleteMessageSeconds int, reason string) (map[string]any, error)
	RemoveMemberTimeout(ctx context.Context, discordGuildID, discordUserID, reason string) (map[string]any, error)
	UnbanMember(ctx context.Context, discordGuildID, discordUserID, reason string) (map[string]any, error)
}

// Messenger sends direct messages to members.
type Messenger interface {
	SendDM(ctx context.Context, discordUserID, message string) (map[string]any, error)
	// PrepareDM opens a DM channel and returns its ID. Quack calls it before a
	// kick or ban, while the bot still shares a guild with the member.
	PrepareDM(ctx context.Context, discordUserID string) (string, error)
	SendPreparedDM(ctx context.Context, channelID, message string) (map[string]any, error)
	// SendCaseNotification sends a case notification with a button linking to
	// the case in the dashboard, where the member can appeal. channelID may be
	// a prepared DM channel or empty.
	SendCaseNotification(ctx context.Context, discordUserID, channelID, message, dashboardBaseURL, guildID, caseID string) (map[string]any, error)
}

// StaffChannelValidator confirms a channel belongs to the guild and is
// private to staff before Quack posts audit events there.
type StaffChannelValidator interface {
	ValidateStaffChannel(ctx context.Context, discordGuildID, channelID string) error
}

// AuditMirrorSender posts a mirrored audit entry. It returns an error
// wrapping ErrAuditMirrorChannelUnavailable when the channel is gone.
type AuditMirrorSender interface {
	SendAuditMirror(context.Context, AuditMirrorMessage) error
}

// AppealNotifier delivers appeal notifications and returns the Discord
// message ID.
type AppealNotifier interface {
	SendAppealMemberNotification(ctx context.Context, discordUserID, body string) (string, error)
	SendAppealStaffNotification(ctx context.Context, guildID, body string) (string, error)
}

// DiscordUserGuild is a guild as listed for a signed-in user.
type DiscordUserGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions uint64 `json:"permissions,string"`
}

// DiscordBotGuild is a guild the bot is in.
type DiscordBotGuild struct {
	ID      string
	Name    string
	Icon    string
	OwnerID string
}

// DiscordGuildAuthorization is a live snapshot of the guild, the bot, the
// acting staff member, and the target member, fetched for one operation.
type DiscordGuildAuthorization struct {
	Guild  DiscordBotGuild
	Actor  DiscordMemberAuthorization
	Bot    DiscordMemberAuthorization
	Target *DiscordMemberAuthorization
}

// DiscordMemberAuthorization is one member's current standing in a guild.
// TopRolePosition drives Discord's role hierarchy checks.
type DiscordMemberAuthorization struct {
	DiscordUserID   string
	DisplayName     string
	PermissionBits  uint64
	TopRolePosition int
	Present         bool
	Bot             bool
}

// DiscordError is a Discord failure classified by the adapter. Retryable
// means repeating the request is safe; OutcomeUncertain means Discord may
// have applied the request anyway.
type DiscordError struct {
	Code             string
	Message          string
	Retryable        bool
	OutcomeUncertain bool
}

func (e DiscordError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

func hasAllBits(bits, required uint64) bool {
	return bits&required == required
}

// hasDiscordPermission reports whether bits grant required. Administrator
// grants everything, as in Discord.
func hasDiscordPermission(bits, required uint64) bool {
	return hasAllBits(bits, permissionAdministrator) || hasAllBits(bits, required)
}

// PermissionBitsString formats permission bits the way Discord's API does,
// as a decimal string, since they overflow JavaScript numbers.
func PermissionBitsString(bits uint64) string {
	return fmt.Sprintf("%d", bits)
}

func discordGuildIconURL(guildID, iconHash string) string {
	if guildID == "" || iconHash == "" {
		return ""
	}
	ext := "png"
	if strings.HasPrefix(iconHash, "a_") {
		ext = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.%s", guildID, iconHash, ext)
}
