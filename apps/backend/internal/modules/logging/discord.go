package logging

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// deliveryPermissions are what Quack needs in a log channel: posting, and
// attaching the full text of an entry too long for one message.
const deliveryPermissions = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles

// errAuditLogRequired explains why logging needs View Audit Log.
var errAuditLogRequired = errors.New("enabling logging requires the View Audit Log permission so bans by other moderators can be logged without duplicating Quack's own actions")

// delivery is the DeliveryClient that posts to Discord.
type delivery struct {
	bot    *discord.Bot
	guilds *modules.Guilds
}

// SendStaffLog posts message after re-checking that the channel is still
// staff-only. It never retries on its own; the service owns the retry
// policy.
func (d delivery) SendStaffLog(ctx context.Context, guildID, channelID string, message discord.Message) error {
	if err := d.ValidateStaffOnlyChannel(ctx, guildID, channelID); err != nil {
		return err
	}
	_, err := d.bot.Send(ctx, channelID, message)
	return err
}

// ValidateStaffOnlyChannel applies discord.Bot.ValidateStaffChannel to the
// guild with internal ID guildID, then checks from live REST state that
// Quack can post and attach files there, so a bad destination fails when
// it is saved rather than on every event.
func (d delivery) ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := d.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	if err := d.bot.ValidateStaffChannel(ctx, discordGuildID, channelID); err != nil {
		return err
	}
	channel, err := d.bot.Session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return err
	}
	permissions, err := d.botPermissions(ctx, discordGuildID, channel)
	if err != nil {
		return err
	}
	if permissions&deliveryPermissions != deliveryPermissions {
		return errors.New("the logging channel must let Quack view it, send messages, and attach files")
	}
	return nil
}

// requireAuditLog checks that Quack can read the guild's audit log, which
// is how bans by other moderators are attributed.
func (d delivery) requireAuditLog(ctx context.Context, discordGuildID string) error {
	permissions, err := d.botPermissions(ctx, discordGuildID, &discordgo.Channel{ID: discordGuildID, GuildID: discordGuildID})
	if err != nil || permissions&discordgo.PermissionViewAuditLogs == 0 {
		return errAuditLogRequired
	}
	return nil
}

// botPermissions computes Quack's permissions in channel from fresh REST
// reads of the guild and Quack's membership. A channel with no overwrites
// gives the guild-level permissions.
func (d delivery) botPermissions(ctx context.Context, discordGuildID string, channel *discordgo.Channel) (int64, error) {
	session := d.bot.Session
	botID := selfID(session)
	if botID == "" {
		user, err := session.User("@me", rest(ctx)...)
		if err != nil {
			return 0, err
		}
		botID = user.ID
	}
	guild, err := session.Guild(discordGuildID, rest(ctx)...)
	if err != nil {
		return 0, err
	}
	member, err := session.GuildMember(discordGuildID, botID, rest(ctx)...)
	if err != nil {
		return 0, err
	}
	// The state indexes members by user, which a member read may omit.
	member.User = &discordgo.User{ID: botID}
	// A throwaway state holding only these reads, so the gateway cache
	// never influences the answer.
	state := discordgo.NewState()
	scoped := *guild
	scoped.Channels = []*discordgo.Channel{channel}
	scoped.Members = []*discordgo.Member{member}
	if err := state.GuildAdd(&scoped); err != nil {
		return 0, err
	}
	return state.UserChannelPermissions(botID, channel.ID)
}

// rest returns the options for one REST call: the caller's context and no
// retries, since the service owns retry policy.
func rest(ctx context.Context) []discordgo.RequestOption {
	return []discordgo.RequestOption{
		discordgo.WithContext(ctx),
		discordgo.WithRestRetries(0),
		discordgo.WithRetryOnRatelimit(false),
	}
}
