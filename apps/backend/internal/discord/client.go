package discord

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const userGuildsURL = "https://discord.com/api/v10/users/@me/guilds"

// UserGuilds lists the guilds of the user who owns the OAuth accessToken.
func (b *Bot) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, errors.New("missing discord access token")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, userGuildsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build user guilds request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	response, err := b.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch user guilds: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return nil, fmt.Errorf("discord user guilds failed with status %d", response.StatusCode)
	}
	var guilds []quack.DiscordUserGuild
	if err := json.NewDecoder(response.Body).Decode(&guilds); err != nil {
		return nil, fmt.Errorf("decode user guilds: %w", err)
	}
	return guilds, nil
}

// BotGuilds lists the guilds the bot is in, from the gateway cache. It is
// used for display only.
func (b *Bot) BotGuilds(context.Context) ([]quack.DiscordBotGuild, error) {
	state := b.Session.State
	state.RLock()
	defer state.RUnlock()
	guilds := make([]quack.DiscordBotGuild, 0, len(state.Guilds))
	for _, guild := range state.Guilds {
		if guild != nil {
			guilds = append(guilds, botGuild(guild))
		}
	}
	return guilds, nil
}

// GuildAuthorization reads the guild, the bot, the actor, and optionally the
// target as Discord has them now, for one protected operation. Gateway state
// answers while the session is live; REST fills in whatever it lacks (see
// live.go).
func (b *Bot) GuildAuthorization(ctx context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guild, err := b.authorizationGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	botID, err := b.botID(ctx)
	if err != nil {
		return nil, quack.ErrAuthorizationUnavailable
	}
	actor, err := b.member(ctx, guild, actorID)
	if err != nil {
		return nil, err
	}
	bot, err := b.member(ctx, guild, botID)
	if err != nil {
		return nil, err
	}
	summary := botGuild(guild)
	summary.RoleIDs = make([]string, 0, len(guild.Roles))
	for _, role := range guild.Roles {
		if role != nil {
			summary.RoleIDs = append(summary.RoleIDs, role.ID)
		}
	}
	snapshot := &quack.DiscordGuildAuthorization{Guild: summary, Actor: actor, Bot: bot}
	if strings.TrimSpace(targetID) != "" {
		target, err := b.member(ctx, guild, targetID)
		if err != nil {
			return nil, err
		}
		snapshot.Target = &target
	}
	return snapshot, nil
}

// TimeoutMember times the member out for exactly durationSeconds.
func (b *Bot) TimeoutMember(ctx context.Context, guildID, userID string, durationSeconds int, reason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	until := time.Now().UTC().Add(time.Duration(durationSeconds) * time.Second)
	if err := b.Session.GuildMemberTimeout(guildID, userID, &until, rest(ctx, discordgo.WithAuditLogReason(reason))...); err != nil {
		return nil, classify("timeout", err, false)
	}
	return map[string]any{"timeout_until": until.Format(time.RFC3339)}, nil
}

// KickMember removes the member from the guild.
func (b *Bot) KickMember(ctx context.Context, guildID, userID, reason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildMemberDeleteWithReason(guildID, userID, reason, rest(ctx)...); err != nil {
		return nil, classify("kick", err, true)
	}
	return map[string]any{"result": "kicked"}, nil
}

// BanMember bans the member. deleteMessageSeconds is sent as is, because
// discordgo's helper only accepts whole days.
func (b *Bot) BanMember(ctx context.Context, guildID, userID string, deleteMessageSeconds int, reason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body := map[string]any{"delete_message_seconds": deleteMessageSeconds}
	bucket := discordgo.EndpointGuildBan(guildID, "")
	endpoint := discordgo.EndpointGuildBan(guildID, userID)
	options := rest(ctx, discordgo.WithAuditLogReason(reason))
	if _, err := b.Session.RequestWithBucketID(http.MethodPut, endpoint, body, bucket, options...); err != nil {
		return nil, classify("ban", err, true)
	}
	return map[string]any{"result": "banned", "delete_message_seconds": deleteMessageSeconds}, nil
}

// RemoveMemberTimeout lifts a timeout as a staff-confirmed reversal.
func (b *Bot) RemoveMemberTimeout(ctx context.Context, guildID, userID, reason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildMemberTimeout(guildID, userID, nil, rest(ctx, discordgo.WithAuditLogReason(reason))...); err != nil {
		return nil, classify("remove_timeout", err, true)
	}
	return map[string]any{"result": "timeout_removed"}, nil
}

// UnbanMember lifts a ban as a staff-confirmed reversal.
func (b *Bot) UnbanMember(ctx context.Context, guildID, userID, reason string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.Session.GuildBanDelete(guildID, userID, rest(ctx, discordgo.WithAuditLogReason(reason))...); err != nil {
		return nil, classify("unban", err, true)
	}
	return map[string]any{"result": "unbanned"}, nil
}

// SendDM sends message to the user's direct-message channel.
func (b *Bot) SendDM(ctx context.Context, userID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel, err := b.Session.UserChannelCreate(userID, rest(ctx)...)
	if err != nil {
		return nil, classify("send_dm_channel", err, false)
	}
	sent, err := b.Send(ctx, channel.ID, Signal("message", message, false))
	if err != nil {
		return nil, classify("send_dm_message", err, false)
	}
	return sentResult(channel.ID, sent), nil
}

// PrepareDM opens the user's direct-message channel. Quack calls it before a
// kick or ban, while the bot still shares a guild with the member.
func (b *Bot) PrepareDM(ctx context.Context, userID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	channel, err := b.Session.UserChannelCreate(userID, rest(ctx)...)
	if err != nil {
		return "", classify("dm_prepare", err, false)
	}
	return channel.ID, nil
}

// SendPreparedDM sends message through a channel opened by PrepareDM.
func (b *Bot) SendPreparedDM(ctx context.Context, channelID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sent, err := b.Send(ctx, channelID, Signal("message", message, false))
	if err != nil {
		return nil, classify("dm_send", err, true)
	}
	return sentResult(channelID, sent), nil
}

// SendCaseNotification sends a case notification with a button that opens
// the case's appeal page in the dashboard. channelID may be a prepared DM
// channel or empty.
func (b *Bot) SendCaseNotification(
	ctx context.Context, userID, channelID, message, dashboardBaseURL, guildID, caseID string,
) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(channelID) == "" {
		channel, err := b.Session.UserChannelCreate(userID, rest(ctx)...)
		if err != nil {
			return nil, classify("dm_prepare", err, false)
		}
		channelID = channel.ID
	}
	entry, err := appealEntryMessage(dashboardBaseURL, guildID, caseID)
	if err != nil {
		return nil, err
	}
	notice := Signal("message", message, false)
	notice.Components = entry.Components
	sent, err := b.Send(ctx, channelID, notice)
	if err != nil {
		return nil, classify("dm_send", err, true)
	}
	return sentResult(channelID, sent), nil
}

// Send posts message to a channel or DM, resolved for the bot's own
// application, with mentions suppressed unless the message allows them.
// Like every REST call here it is attempted once; the caller owns retries.
func (b *Bot) Send(ctx context.Context, channelID string, message Message) (*discordgo.Message, error) {
	message.Ephemeral = false
	params := message.ForApplication(b.applicationID(ctx)).sendParams()
	return b.Session.ChannelMessageSendComplex(channelID, params, rest(ctx)...)
}

// Edit replaces a message the bot posted with message, resolved like Send.
// It needs no interaction token, so it works on messages of any age.
func (b *Bot) Edit(ctx context.Context, channelID, messageID string, message Message) (*discordgo.Message, error) {
	edit := EditMessage(message).ForApplication(b.applicationID(ctx)).webhookEdit()
	return b.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              messageID,
		Channel:         channelID,
		Content:         edit.Content,
		Embeds:          edit.Embeds,
		Components:      edit.Components,
		Files:           edit.Files,
		Attachments:     edit.Attachments,
		AllowedMentions: edit.AllowedMentions,
	}, rest(ctx)...)
}

// applicationID is the ID icons and command mentions resolve against: the
// bot user's ID, which Discord gives the application too. It is "" when
// Discord cannot say, and messages then read without icons.
func (b *Bot) applicationID(ctx context.Context) string {
	id, _ := b.botID(ctx)
	return id
}

// sentResult is the result Quack records for a delivered DM.
func sentResult(channelID string, sent *discordgo.Message) map[string]any {
	result := map[string]any{"channel_id": channelID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result
}

// authorizationGuild returns the guild and its roles from gateway state, or
// from REST when state cannot answer.
func (b *Bot) authorizationGuild(ctx context.Context, guildID string) (*discordgo.Guild, error) {
	if guild, ok := b.stateGuild(guildID); ok {
		return guild, nil
	}
	guild, err := b.Session.Guild(guildID, rest(ctx)...)
	if err != nil {
		switch statusCode(err) {
		case http.StatusForbidden, http.StatusNotFound:
			return nil, quack.ErrBotNotInGuild
		}
		return nil, quack.ErrAuthorizationUnavailable
	}
	return guild, nil
}

// member reads one member's current standing, from gateway state when it
// has them and from REST otherwise. Discord's unknown-member response means
// the user is not in the guild, which is not an error; absence is never
// cached.
func (b *Bot) member(ctx context.Context, guild *discordgo.Guild, userID string) (quack.DiscordMemberAuthorization, error) {
	userID = strings.TrimSpace(userID)
	absent := quack.DiscordMemberAuthorization{DiscordUserID: userID}
	if userID == "" {
		return absent, nil
	}
	if err := ctx.Err(); err != nil {
		return absent, err
	}
	if member, ok := b.liveMember(guild.ID, userID); ok {
		return memberAuthorization(guild, member), nil
	}
	generation := b.live.generation(guild.ID)
	member, err := b.Session.GuildMember(guild.ID, userID, rest(ctx)...)
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return absent, nil
		}
		return absent, quack.ErrAuthorizationUnavailable
	}
	if member.GuildID == "" {
		member.GuildID = guild.ID
	}
	b.live.remember(member, generation, time.Now())
	return memberAuthorization(guild, member), nil
}

// memberAuthorization computes a member's guild-level permissions, top
// role position, and roles. Only roles the guild still has count, so a
// role deleted while state or REST still lists it on the member grants
// nothing. The owner and administrators get every permission, as in
// Discord.
func memberAuthorization(guild *discordgo.Guild, member *discordgo.Member) quack.DiscordMemberAuthorization {
	if member == nil || member.User == nil {
		return quack.DiscordMemberAuthorization{}
	}
	var permissions int64
	var roleIDs []string
	topPosition := 0
	held := make(map[string]bool, len(member.Roles))
	for _, roleID := range member.Roles {
		held[roleID] = true
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if role.ID == guild.ID {
			permissions |= role.Permissions
		}
		if held[role.ID] && role.ID != guild.ID {
			permissions |= role.Permissions
			topPosition = max(topPosition, role.Position)
			roleIDs = append(roleIDs, role.ID)
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		permissions |= discordgo.PermissionAll
	}
	return quack.DiscordMemberAuthorization{
		DiscordUserID:   member.User.ID,
		DisplayName:     displayName(member),
		PermissionBits:  uint64(permissions),
		TopRolePosition: topPosition,
		RoleIDs:         roleIDs,
		Present:         true,
		Bot:             member.User.Bot,
	}
}

// displayName prefers the guild nickname, then the global name, then the
// username. member.User must be set.
func displayName(member *discordgo.Member) string {
	if name := strings.TrimSpace(member.Nick); name != "" {
		return name
	}
	if name := strings.TrimSpace(member.User.GlobalName); name != "" {
		return name
	}
	return strings.TrimSpace(member.User.Username)
}

// botGuild converts a discordgo guild to the quack summary of it.
func botGuild(guild *discordgo.Guild) quack.DiscordBotGuild {
	return quack.DiscordBotGuild{
		ID:          guild.ID,
		Name:        guild.Name,
		Icon:        guild.Icon,
		OwnerID:     guild.OwnerID,
		MFARequired: guild.MfaLevel == discordgo.MfaLevelElevated,
	}
}

// GuildRoles lists the guild's roles except @everyone, highest first, for
// staff role pickers and settings validation. Gateway state answers while
// the session is live; REST otherwise.
func (b *Bot) GuildRoles(ctx context.Context, guildID string) ([]quack.DiscordRole, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guild, err := b.authorizationGuild(ctx, guildID)
	if err != nil {
		return nil, err
	}
	roles := make([]quack.DiscordRole, 0, len(guild.Roles))
	for _, role := range guild.Roles {
		if role == nil || role.ID == guild.ID {
			continue
		}
		roles = append(roles, quack.DiscordRole{
			ID: role.ID, Name: role.Name, Color: role.Color, Position: role.Position, Managed: role.Managed,
		})
	}
	// Position ties, which Discord allows, fall back to snowflake age.
	slices.SortFunc(roles, func(a, b quack.DiscordRole) int {
		return cmp.Or(
			cmp.Compare(b.Position, a.Position),
			cmp.Compare(len(a.ID), len(b.ID)),
			strings.Compare(a.ID, b.ID),
		)
	})
	return roles, nil
}
