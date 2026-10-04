package honeypot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// channelValidator is the ChannelValidator over live Discord state.
type channelValidator struct {
	session *discordgo.Session
	guilds  *modules.Guilds
}

// ValidateHoneypotChannel requires a text channel in the guild where Quack
// can post the warning, read the bait message as evidence, and delete it,
// checked live. The error names any permission Quack is missing.
func (v channelValidator) ValidateHoneypotChannel(ctx context.Context, guildID, channelID string) error {
	discordGuildID, err := v.guilds.DiscordID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return err
	}
	channel, err := v.session.Channel(strings.TrimSpace(channelID), rest(ctx)...)
	if err != nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
		return ErrChannelUnavailable
	}
	guild, member, err := botMember(ctx, v.session, discordGuildID)
	if err != nil {
		return err
	}
	permissions := channelPermissions(guild, channel, member)
	var missing []string
	for _, required := range []struct {
		bit  int64
		name string
	}{
		{discordgo.PermissionViewChannel, "View Channel"},
		{discordgo.PermissionSendMessages, "Send Messages"},
		{discordgo.PermissionReadMessageHistory, "Read Message History"},
		{discordgo.PermissionManageMessages, "Manage Messages"},
	} {
		if permissions&required.bit == 0 {
			missing = append(missing, required.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: Quack needs %s in the honeypot channel", ErrChannelUnavailable, strings.Join(missing, ", "))
	}
	return nil
}

// botMember loads the guild and the bot's current membership in it.
func botMember(ctx context.Context, session *discordgo.Session, discordGuildID string) (*discordgo.Guild, *discordgo.Member, error) {
	id := botID(session)
	if id == "" {
		user, err := session.User("@me", rest(ctx)...)
		if err != nil {
			return nil, nil, errors.New("current Discord bot identity is unavailable")
		}
		id = user.ID
	}
	guild, err := session.Guild(discordGuildID, rest(ctx)...)
	if err != nil {
		return nil, nil, errors.New("current Discord guild is unavailable")
	}
	member, err := session.GuildMember(discordGuildID, id, rest(ctx)...)
	if err != nil || member.User == nil || member.User.ID != id {
		return nil, nil, errors.New("current Discord bot membership is unavailable")
	}
	return guild, member, nil
}

// botID returns the bot's user ID once the gateway has delivered it.
func botID(session *discordgo.Session) string {
	if session.State != nil && session.State.User != nil {
		return session.State.User.ID
	}
	return ""
}

// rest returns the options for one REST call: the caller's context and no
// retries, since the honeypot's loops own retry policy.
func rest(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}

// restCode returns the Discord JSON error code in err, or 0.
func restCode(err error) int {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Message != nil {
		return restErr.Message.Code
	}
	return 0
}

// messageURL is the jump link to a message.
func messageURL(guildID, channelID, messageID string) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", guildID, channelID, messageID)
}

// projectMessage builds the trap policy's view of a message from the event
// plus freshly loaded guild, channel, and member state.
func projectMessage(guildID string, event *discordgo.MessageCreate, guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member, botID string) (Message, error) {
	if strings.TrimSpace(guildID) == "" || event.Message == nil || event.GuildID == "" ||
		guild == nil || guild.ID != event.GuildID ||
		channel == nil || channel.GuildID != event.GuildID ||
		member == nil || member.User == nil || member.User.ID == "" {
		return Message{}, errors.New("honeypot message projection is incomplete")
	}
	if event.Author == nil || event.Author.ID != member.User.ID {
		return Message{}, errors.New("honeypot message author does not match current member")
	}
	return Message{
		GuildID:             strings.TrimSpace(guildID),
		ChannelDiscordID:    channel.ID,
		MessageDiscordID:    event.ID,
		AuthorDiscordUserID: member.User.ID,
		MessageURL:          messageURL(event.GuildID, channel.ID, event.ID),
		IsBot:               member.User.Bot,
		IsQuack:             member.User.ID == botID,
		IsWebhook:           event.WebhookID != "",
		AuthorCanModerate:   canModerate(guild, member),
	}, nil
}

// canModerate reports guild-wide moderation authority (Administrator or
// Moderate Members, or ownership), the same baseline cases and appeals use.
// Trap-channel overwrites neither grant nor remove it, so they cannot
// exempt an ordinary member or expose a moderator.
func canModerate(guild *discordgo.Guild, member *discordgo.Member) bool {
	permissions := channelPermissions(guild, &discordgo.Channel{GuildID: guild.ID}, member)
	return permissions&(discordgo.PermissionAdministrator|discordgo.PermissionModerateMembers) != 0
}

// channelPermissions computes a member's permissions in a channel the way
// Discord does: base role permissions, then @everyone, role, and member
// overwrites in that order. The owner and administrators get everything.
func channelPermissions(guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member) int64 {
	if guild == nil || channel == nil || member == nil || member.User == nil {
		return 0
	}
	permissions := int64(0)
	roles := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roles[roleID] = struct{}{}
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if _, ok := roles[role.ID]; ok || role.ID == guild.ID {
			permissions |= role.Permissions
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		return discordgo.PermissionAll
	}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guild.ID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	roleDeny, roleAllow := int64(0), int64(0)
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type != discordgo.PermissionOverwriteTypeRole {
			continue
		}
		if _, ok := roles[overwrite.ID]; ok {
			roleDeny |= overwrite.Deny
			roleAllow |= overwrite.Allow
		}
	}
	permissions = permissions&^roleDeny | roleAllow
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == member.User.ID && overwrite.Type == discordgo.PermissionOverwriteTypeMember {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	return permissions
}
