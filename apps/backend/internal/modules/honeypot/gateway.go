package honeypot

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// onMessageCreate projects a message in a guild with the honeypot on and
// queues it. The author's roles and permissions are fetched fresh rather
// than trusted from the event.
func (m *Module) onMessageCreate(session *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || event.Author == nil {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	configuration, err := m.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	channel, err := session.Channel(event.ChannelID)
	if err != nil || channel.GuildID != event.GuildID {
		return
	}
	guild, err := session.Guild(event.GuildID)
	if err != nil {
		return
	}
	member, err := session.GuildMember(event.GuildID, event.Author.ID)
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
