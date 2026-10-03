package honeypot

import (
	"context"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
)

// gatewayTimeout bounds the lookups a gateway handler makes before it hands
// work off or gives up.
const gatewayTimeout = 5 * time.Second

// onMessageCreate projects a message in an enabled guild's trap channel and
// queues it. Messages elsewhere, and Quack's own and webhook posts, are
// dropped before any Discord call. The author's roles and permissions are
// fetched fresh rather than trusted from the event.
func (m *Module) onMessageCreate(session *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || event.Author == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
	defer cancel()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	settings, enabled, err := m.service.loadSettings(ctx, guildID)
	if err != nil || !enabled || settings.ChannelDiscordID != event.ChannelID {
		return
	}
	if event.Author.ID == botID(session) || event.WebhookID != "" {
		return
	}
	channel, err := session.Channel(event.ChannelID, discordgo.WithContext(ctx))
	if err != nil || channel.GuildID != event.GuildID {
		return
	}
	guild, err := session.Guild(event.GuildID, discordgo.WithContext(ctx))
	if err != nil {
		return
	}
	member, err := session.GuildMember(event.GuildID, event.Author.ID, discordgo.WithContext(ctx))
	if err != nil {
		return
	}
	message, err := projectMessage(guildID, event, guild, channel, member, botID(session))
	if err != nil {
		return
	}
	// A full queue sheds the message: honeypot traffic must never back up
	// the gateway.
	m.pool.Submit(message)
}

// onMessageDelete reposts the warning if it was the message deleted.
func (m *Module) onMessageDelete(_ *discordgo.Session, event *discordgo.MessageDelete) {
	if event.Message == nil || event.GuildID == "" {
		return
	}
	m.warningDeleted(event.GuildID, event.ChannelID, []string{event.ID})
}

// onMessageDeleteBulk reposts the warning if a bulk delete included it.
func (m *Module) onMessageDeleteBulk(_ *discordgo.Session, event *discordgo.MessageDeleteBulk) {
	if event.GuildID == "" {
		return
	}
	m.warningDeleted(event.GuildID, event.ChannelID, event.Messages)
}

// warningDeleted queues a warning refresh when a moderator or another bot
// deleted the guild's warning. It never changes a moderation outcome.
func (m *Module) warningDeleted(discordGuildID, channelID string, messageIDs []string) {
	ctx, cancel := context.WithTimeout(context.Background(), gatewayTimeout)
	defer cancel()
	guildID, err := m.guilds.InternalID(ctx, discordGuildID)
	if err != nil {
		return
	}
	if err := m.warnings.deleted(ctx, guildID, channelID, messageIDs); err != nil {
		slog.WarnContext(ctx, "Could not restore deleted honeypot warning", "guild_id", guildID, "error", err)
	}
}

// onChannelDelete turns the honeypot off if its trap channel was deleted.
func (m *Module) onChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	_ = m.service.HandleDeletedChannel(ctx, guildID, event.ID)
}

// onGuildDelete turns the honeypot off when Quack leaves the guild, keeping
// its settings for a repair after a rejoin. An outage is not a departure.
func (m *Module) onGuildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalIDAny(ctx, event.ID)
	if err != nil {
		return
	}
	settings, enabled, err := m.service.loadSettings(ctx, guildID)
	if err != nil || !enabled || settings.ChannelDiscordID == "" {
		return
	}
	_ = m.service.HandleDeletedChannel(ctx, guildID, settings.ChannelDiscordID)
}
