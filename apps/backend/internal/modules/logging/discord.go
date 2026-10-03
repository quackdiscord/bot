package logging

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// delivery is the DeliveryClient that posts to Discord.
type delivery struct {
	bot    *discord.Bot
	guilds *modules.Guilds
}

// SendStaffLog posts payload with mentions suppressed, after re-checking
// that the channel is still staff-only. It never retries on its own; the
// service owns the retry policy.
func (d delivery) SendStaffLog(ctx context.Context, guildID, channelID, payload string) error {
	if err := d.ValidateStaffOnlyChannel(ctx, guildID, channelID); err != nil {
		return err
	}
	_, err := d.bot.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content:         payload,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// ValidateStaffOnlyChannel applies discord.Bot.ValidateStaffChannel to the
// guild with internal ID guildID, then checks that the bot can post there,
// so a bad destination fails when it is saved rather than on every event.
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
	session := d.bot.Session
	if session.State == nil || session.State.User == nil {
		return errors.New("discord bot identity is unavailable")
	}
	permissions, err := session.UserChannelPermissions(session.State.User.ID, channelID)
	if err != nil {
		return err
	}
	if permissions&discordgo.PermissionViewChannel == 0 || permissions&discordgo.PermissionSendMessages == 0 {
		return errors.New("discord bot cannot deliver to logging destination")
	}
	return nil
}
