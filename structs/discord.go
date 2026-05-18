package structs

import (
	"time"

	"github.com/bwmarrin/discordgo"
)

type DiscordCommand struct {
	*discordgo.ApplicationCommand
	Handler func(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse
}

type DiscordEvent struct {
	Handler any
}

// internal Discord snapshot (non-schema)
type DiscordGuild struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description"`
	IconURL     string               `json:"icon_url"`
	OwnerID     string               `json:"owner_id"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	DeletedAt   *time.Time           `json:"deleted_at,omitempty"`
	Settings    DiscordGuildSettings `json:"settings"`
}

// this will change a lot
type DiscordGuildSettings struct {
	ID      string `json:"id"`
	GuildID string `json:"guild_id"`
}
