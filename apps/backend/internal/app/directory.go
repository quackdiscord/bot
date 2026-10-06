package app

import (
	"context"

	"github.com/quackdiscord/bot/internal/api"
	"github.com/quackdiscord/bot/internal/discord"
)

// directory serves api.Directory from the Discord adapter. The two packages
// keep their own types so that neither imports the other; this converts
// between them.
type directory struct {
	bot *discord.Bot
}

// SearchMembers implements api.Directory.
func (d directory) SearchMembers(ctx context.Context, discordGuildID, query string, limit int) ([]api.DirectoryUser, error) {
	users, err := d.bot.SearchMembers(ctx, discordGuildID, query, limit)
	return directoryUsers(users), err
}

// LookupUsers implements api.Directory.
func (d directory) LookupUsers(ctx context.Context, discordGuildID string, userIDs []string) ([]api.DirectoryUser, error) {
	users, err := d.bot.LookupUsers(ctx, discordGuildID, userIDs)
	return directoryUsers(users), err
}

// Channels implements api.Directory.
func (d directory) Channels(ctx context.Context, discordGuildID string) ([]api.DirectoryChannel, error) {
	channels, err := d.bot.Channels(ctx, discordGuildID)
	if err != nil {
		return nil, err
	}
	out := make([]api.DirectoryChannel, len(channels))
	for i, c := range channels {
		out[i] = api.DirectoryChannel{
			ID: c.ID, Name: c.Name, Type: api.ChannelType(c.Type), ParentID: c.ParentID, Position: c.Position,
		}
	}
	return out, nil
}

func directoryUsers(users []discord.DirectoryUser) []api.DirectoryUser {
	if users == nil {
		return nil
	}
	out := make([]api.DirectoryUser, len(users))
	for i, u := range users {
		out[i] = api.DirectoryUser{
			ID: u.ID, Username: u.Username, GlobalName: u.GlobalName, Nick: u.Nick,
			DisplayName: u.DisplayName, AvatarURL: u.AvatarURL, Bot: u.Bot, InGuild: u.InGuild,
		}
	}
	return out
}
