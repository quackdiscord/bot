package modules

import (
	"context"
	"errors"

	"github.com/quackdiscord/bot/internal/quack"
)

// ErrUnknownGuild is returned for a guild Quack has no record of.
var ErrUnknownGuild = errors.New("guild is not registered")

// GuildStore looks up guild records and their core settings.
type GuildStore interface {
	GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error)
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error)
}

// Guilds maps between Discord guild IDs, which gateway events and Discord
// calls use, and Quack's internal guild IDs, which module tables use.
type Guilds struct{ store GuildStore }

// NewGuilds returns a Guilds over store.
func NewGuilds(store GuildStore) *Guilds { return &Guilds{store: store} }

// InternalID returns the internal ID of an active guild. Events from guilds
// Quack has left resolve to ErrUnknownGuild and are dropped.
func (g *Guilds) InternalID(ctx context.Context, discordGuildID string) (string, error) {
	guild, err := g.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil {
		return "", err
	}
	if guild == nil || !guild.IsActive {
		return "", ErrUnknownGuild
	}
	return guild.ID, nil
}

// InternalIDAny is InternalID for departure cleanup: it also resolves an
// inactive guild, since the core lifecycle handler may already have marked
// it inactive.
func (g *Guilds) InternalIDAny(ctx context.Context, discordGuildID string) (string, error) {
	guild, err := g.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil {
		return "", err
	}
	if guild == nil {
		return "", ErrUnknownGuild
	}
	return guild.ID, nil
}

// StaffRoles returns the staff roles a guild configured, or none for a
// guild Quack has no settings for. Modules use them with
// quack.StaffRoles.IsDiscordStaff to recognize staff in Discord.
func (g *Guilds) StaffRoles(ctx context.Context, discordGuildID string) (quack.StaffRoles, error) {
	guild, err := g.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil || guild == nil {
		return quack.StaffRoles{}, err
	}
	settings, err := g.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return quack.StaffRoles{}, err
	}
	return settings.StaffRoles(), nil
}

// DiscordID returns the Discord ID of an active guild.
func (g *Guilds) DiscordID(ctx context.Context, guildID string) (string, error) {
	guild, err := g.store.GetGuildByID(ctx, guildID)
	if err != nil {
		return "", err
	}
	if guild == nil || !guild.IsActive {
		return "", ErrUnknownGuild
	}
	return guild.DiscordGuildID, nil
}
