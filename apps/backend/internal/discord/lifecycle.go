package discord

import (
	"context"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// HandleGuildLifecycle keeps Quack's guild records in step with the
// gateway: joins, renames, departures, and deleted channels. Call it before
// Open so the initial GuildCreate events are not missed.
func HandleGuildLifecycle(bot *Bot, services *quack.Services) {
	l := &lifecycle{guilds: services.Guilds, evidence: services.Evidence}
	bot.Session.AddHandler(l.guildCreate)
	bot.Session.AddHandler(l.guildUpdate)
	bot.Session.AddHandler(l.guildDelete)
	bot.Session.AddHandler(l.channelDelete)
}

// lifecycle turns guild and channel gateway events into idempotent guild
// service calls.
type lifecycle struct {
	guilds   *quack.GuildService
	evidence *quack.EvidenceService
}

// guildCreate installs a new guild or reactivates a known one, repairing
// channel references from the full channel list GuildCreate carries.
func (l *lifecycle) guildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx := context.Background()
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
	result, err := l.guilds.BootstrapDiscordGuild(ctx, lifecycleInput(event.Guild, ids))
	if err != nil {
		slog.Error("Failed to bootstrap Discord guild", "error", err, "guild_id", event.ID)
		return
	}
	if _, err := l.evidence.EnsureGuildEvidenceChannel(ctx, result.Guild, result.Settings); err != nil {
		slog.Error("Failed to ensure managed evidence channel", "error", err, "guild_id", event.ID)
	}
}

// guildUpdate refreshes name, icon, and owner. GuildUpdate has no channel
// list, so it never clears channel references.
func (l *lifecycle) guildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx := context.Background()
	result, err := l.guilds.BootstrapDiscordGuild(ctx, lifecycleInput(event.Guild, nil))
	if err != nil {
		slog.Error("Failed to refresh Discord guild", "error", err, "guild_id", event.ID)
		return
	}
	if _, err := l.evidence.EnsureGuildEvidenceChannel(ctx, result.Guild, result.Settings); err != nil {
		slog.Error("Detected managed evidence channel drift", "error", err, "guild_id", event.ID)
	}
}

// guildDelete marks the guild inactive when the bot leaves. An outage also
// arrives as GuildDelete, flagged unavailable, and is ignored.
func (l *lifecycle) guildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	if _, err := l.guilds.DeactivateDiscordGuild(context.Background(), event.ID); err != nil {
		slog.Error("Failed to deactivate departed Discord guild", "error", err, "guild_id", event.ID)
	}
}

// channelDelete clears settings that pointed at the deleted channel and
// recreates the evidence channel if that was the one deleted.
func (l *lifecycle) channelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil || event.GuildID == "" {
		return
	}
	ctx := context.Background()
	if _, err := l.guilds.ClearDeletedChannel(ctx, event.GuildID, event.ID); err != nil {
		slog.Error("Failed to clear deleted Discord channel reference", "error", err, "guild_id", event.GuildID, "channel_id", event.ID)
	}
	if _, err := l.evidence.RepairDiscordGuildEvidenceChannel(ctx, event.GuildID); err != nil {
		slog.Error("Failed to repair managed evidence channel after deletion", "error", err, "guild_id", event.GuildID)
	}
}

func lifecycleInput(guild *discordgo.Guild, channelIDs []string) quack.DiscordGuildLifecycleInput {
	return quack.DiscordGuildLifecycleInput{
		DiscordGuildID:         guild.ID,
		Name:                   guild.Name,
		Icon:                   guild.Icon,
		OwnerDiscordUserID:     guild.OwnerID,
		KnownChannelDiscordIDs: channelIDs,
	}
}
