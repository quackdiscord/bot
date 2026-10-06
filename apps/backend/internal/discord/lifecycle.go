package discord

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// HandleGuildLifecycle keeps Quack's guild records in step with the
// gateway: joins, renames, departures, and deleted channels. Call it before
// Open so the initial GuildCreate events are not missed.
func HandleGuildLifecycle(bot *Bot, services *quack.Services) {
	l := &lifecycle{guilds: services.Guilds}
	bot.Session.AddHandler(l.guildCreate)
	bot.Session.AddHandler(l.guildUpdate)
	bot.Session.AddHandler(l.guildDelete)
	bot.Session.AddHandler(l.channelDelete)
}

// lifecycleTimeout bounds the handling of one event, including waits for
// Discord rate limits, so one stuck guild cannot hold up the rest.
const lifecycleTimeout = time.Minute

// lifecycle turns guild and channel gateway events into idempotent guild
// service calls.
//
// Handlers run one at a time. On connect Discord sends a GuildCreate for
// every guild at once, and bootstrapping them in parallel deadlocks in MySQL.
type lifecycle struct {
	guilds *quack.GuildService
	mu     sync.Mutex
}

// begin serializes one event and returns its context. Call the returned
// function when the event is handled.
func (l *lifecycle) begin() (context.Context, func()) {
	l.mu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), lifecycleTimeout)
	return ctx, func() {
		cancel()
		l.mu.Unlock()
	}
}

// guildCreate installs a new guild or reactivates a known one, repairing
// channel references from the full channel list GuildCreate carries.
func (l *lifecycle) guildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx, done := l.begin()
	defer done()
	// A nil list means "unknown" and keeps every reference; an empty one
	// means the guild has no channels.
	var ids []string
	if event.Channels != nil {
		ids = make([]string, 0, len(event.Channels))
	}
	for _, channel := range event.Channels {
		if channel != nil && channel.ID != "" {
			ids = append(ids, channel.ID)
		}
	}
	if _, err := l.guilds.BootstrapDiscordGuild(ctx, lifecycleInput(event.Guild, ids)); err != nil {
		slog.Error("Failed to bootstrap Discord guild", "error", err, "guild_id", event.ID)
	}
}

// guildUpdate refreshes name, icon, and owner. GuildUpdate has no channel
// list, so it never clears channel references.
func (l *lifecycle) guildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx, done := l.begin()
	defer done()
	if _, err := l.guilds.BootstrapDiscordGuild(ctx, lifecycleInput(event.Guild, nil)); err != nil {
		slog.Error("Failed to refresh Discord guild", "error", err, "guild_id", event.ID)
	}
}

// guildDelete marks the guild inactive when the bot leaves. An outage also
// arrives as GuildDelete, flagged unavailable, and is ignored.
func (l *lifecycle) guildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx, done := l.begin()
	defer done()
	if _, err := l.guilds.DeactivateDiscordGuild(ctx, event.ID); err != nil {
		slog.Error("Failed to deactivate departed Discord guild", "error", err, "guild_id", event.ID)
	}
}

// channelDelete clears settings that pointed at the deleted channel. A
// deleted evidence channel is not recreated; admins set up a new one.
func (l *lifecycle) channelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil || event.GuildID == "" {
		return
	}
	ctx, done := l.begin()
	defer done()
	if _, err := l.guilds.ClearDeletedChannel(ctx, event.GuildID, event.ID); err != nil {
		slog.Error("Failed to clear deleted Discord channel reference", "error", err, "guild_id", event.GuildID, "channel_id", event.ID)
	}
}

// lifecycleInput describes guild for the guild service. channelIDs is the
// guild's full channel list, or nil when the event does not carry one.
func lifecycleInput(guild *discordgo.Guild, channelIDs []string) quack.DiscordGuildLifecycleInput {
	return quack.DiscordGuildLifecycleInput{
		DiscordGuildID:         guild.ID,
		Name:                   guild.Name,
		Icon:                   guild.Icon,
		OwnerDiscordUserID:     guild.OwnerID,
		KnownChannelDiscordIDs: channelIDs,
	}
}
