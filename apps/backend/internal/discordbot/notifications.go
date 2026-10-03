package discordbot

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui/views"
	"github.com/quackdiscord/bot/internal/quack"
)

// SendDM sends dm through the configured external gateway.
func (b *Bot) SendDM(ctx context.Context, userID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.Session == nil {
		return nil, quack.DiscordError{Code: "discord_session_unavailable", Message: "discord session is unavailable", Retryable: true}
	}
	channel, err := b.Session.UserChannelCreate(userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordError("send_dm_channel", err)
	}
	sent, err := b.Session.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{Content: message, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordError("send_dm_message", err)
	}
	result := map[string]any{"channel_id": channel.ID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result, nil
}

// PrepareDM opens the target's direct-message channel before an irreversible membership action.
func (b *Bot) PrepareDM(ctx context.Context, userID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b == nil || b.Session == nil {
		return "", quack.DiscordError{Code: "discord_session_unavailable", Message: "Discord is unavailable", Retryable: true}
	}
	channel, err := b.Session.UserChannelCreate(userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", classifyDiscordOperation("dm_prepare", err, false)
	}
	return channel.ID, nil
}

// SendPreparedDM sends one final structured case notification through a pre-opened channel.
func (b *Bot) SendPreparedDM(ctx context.Context, channelID, message string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sent, err := b.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: message, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("dm_send", err, true)
	}
	result := map[string]any{"channel_id": channelID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result, nil
}

// SendCaseNotification sends the case body with a secure dashboard appeal
// button through a prepared or newly opened direct-message channel.
func (b *Bot) SendCaseNotification(ctx context.Context, userID, channelID, message, dashboardBaseURL, guildID, caseID string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil || b.Session == nil {
		return nil, quack.DiscordError{Code: "discord_session_unavailable", Message: "Discord is unavailable", Retryable: true}
	}
	if strings.TrimSpace(channelID) == "" {
		channel, err := b.Session.UserChannelCreate(userID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			return nil, classifyDiscordOperation("dm_prepare", err, false)
		}
		channelID = channel.ID
	}
	entry, err := views.AppealEntryMessage(dashboardBaseURL, guildID, caseID)
	if err != nil {
		return nil, err
	}
	sent, err := b.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{Content: message, Components: entry.Components, AllowedMentions: &discordgo.MessageAllowedMentions{}}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("dm_send", err, true)
	}
	result := map[string]any{"channel_id": channelID}
	if sent != nil {
		result["message_id"] = sent.ID
	}
	return result, nil
}
