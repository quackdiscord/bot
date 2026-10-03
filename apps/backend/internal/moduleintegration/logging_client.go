package moduleintegration

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
)

// loggingDiscordClient sends already-redacted payloads only to channels that
// pass the core staff-only check.
type loggingDiscordClient struct {
	bot      *discord.Bot
	resolver guildResolver
}

// SendStaffLog delivers one mention-suppressed message to a validated channel.
func (c loggingDiscordClient) SendStaffLog(ctx context.Context, guildID, channelID, payload string) error {
	if err := c.ValidateStaffOnlyChannel(ctx, guildID, channelID); err != nil {
		return err
	}
	_, err := c.bot.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: payload, AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// ValidateStaffOnlyChannel applies discord.Bot.ValidateStaffChannel to the
// guild with internal ID guildID, then checks that the bot can post there.
func (c loggingDiscordClient) ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := c.resolver.discordID(ctx, guildID)
	if err != nil {
		return err
	}
	if err := c.bot.ValidateStaffChannel(ctx, discordGuildID, channelID); err != nil {
		return err
	}
	return c.checkBotDelivery(channelID)
}

// checkBotDelivery rejects a destination the bot cannot view or post in, so
// logging settings fail at save time instead of on every event.
func (c loggingDiscordClient) checkBotDelivery(channelID string) error {
	session := c.bot.Session
	if session.State == nil || session.State.User == nil {
		return errors.New("Discord bot identity is unavailable")
	}
	permissions, err := session.UserChannelPermissions(session.State.User.ID, channelID)
	if err != nil {
		return err
	}
	if permissions&discordgo.PermissionViewChannel == 0 || permissions&discordgo.PermissionSendMessages == 0 {
		return errors.New("Discord bot cannot deliver to logging destination")
	}
	return nil
}
