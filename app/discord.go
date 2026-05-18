package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	runtimeDiscord "github.com/quackdiscord/bot/discord"
)

const discordUserGuildsURL = "https://discord.com/api/v10/users/@me/guilds"

type DiscordClient interface {
	UserGuilds(ctx context.Context, accessToken string) ([]DiscordUserGuild, error)
	BotGuilds(ctx context.Context) ([]DiscordBotGuild, error)
	BotGuild(ctx context.Context, discordGuildID string) (*DiscordBotGuild, error)
}

type DiscordUserGuild struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Icon        string `json:"icon"`
	Owner       bool   `json:"owner"`
	Permissions uint64 `json:"permissions,string"`
}

type DiscordBotGuild struct {
	ID      string
	Name    string
	Icon    string
	OwnerID string
}

type DiscordAPIClient struct {
	HTTPClient *http.Client
}

func NewDiscordAPIClient() *DiscordAPIClient {
	return &DiscordAPIClient{HTTPClient: http.DefaultClient}
}

func (c *DiscordAPIClient) UserGuilds(ctx context.Context, accessToken string) ([]DiscordUserGuild, error) {
	if strings.TrimSpace(accessToken) == "" {
		return nil, errors.New("missing discord access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, discordUserGuildsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create user guilds request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch user guilds: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("discord user guilds failed with status %d", resp.StatusCode)
	}

	var guilds []DiscordUserGuild
	if err := json.NewDecoder(resp.Body).Decode(&guilds); err != nil {
		return nil, fmt.Errorf("decode user guilds: %w", err)
	}

	return guilds, nil
}

func (c *DiscordAPIClient) BotGuild(ctx context.Context, discordGuildID string) (*DiscordBotGuild, error) {
	if runtimeDiscord.Session == nil {
		return nil, ErrBotNotInGuild
	}

	if runtimeDiscord.Session.State != nil {
		guild, err := runtimeDiscord.Session.State.Guild(discordGuildID)
		if err == nil && guild != nil {
			return &DiscordBotGuild{
				ID:      guild.ID,
				Name:    guild.Name,
				Icon:    guild.Icon,
				OwnerID: guild.OwnerID,
			}, nil
		}
	}

	guild, err := runtimeDiscord.Session.Guild(discordGuildID)
	if err != nil {
		return nil, ErrBotNotInGuild
	}

	return &DiscordBotGuild{
		ID:      guild.ID,
		Name:    guild.Name,
		Icon:    guild.Icon,
		OwnerID: guild.OwnerID,
	}, nil
}

func (c *DiscordAPIClient) BotGuilds(ctx context.Context) ([]DiscordBotGuild, error) {
	if runtimeDiscord.Session == nil || runtimeDiscord.Session.State == nil {
		return []DiscordBotGuild{}, nil
	}

	runtimeDiscord.Session.State.RLock()
	defer runtimeDiscord.Session.State.RUnlock()

	guilds := make([]DiscordBotGuild, 0, len(runtimeDiscord.Session.State.Guilds))
	for _, guild := range runtimeDiscord.Session.State.Guilds {
		if guild == nil {
			continue
		}
		guilds = append(guilds, DiscordBotGuild{
			ID:      guild.ID,
			Name:    guild.Name,
			Icon:    guild.Icon,
			OwnerID: guild.OwnerID,
		})
	}

	return guilds, nil
}

func discordGuildIconURL(guildID, iconHash string) string {
	if guildID == "" || iconHash == "" {
		return ""
	}

	ext := "png"
	if strings.HasPrefix(iconHash, "a_") {
		ext = "gif"
	}

	return fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.%s", guildID, iconHash, ext)
}
