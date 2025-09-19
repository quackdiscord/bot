package events

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onGuildDelete)
}

func onGuildDelete(s *discordgo.Session, gd *discordgo.GuildDelete) {
	// delete the guild
	err := storage.DeleteGuild(gd.Guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete guild", err)
	}

	// update the guild count channel
	_, err = s.ChannelEdit(config.Bot.GuildCountChannel, &discordgo.ChannelEdit{
		Name: fmt.Sprintf("%d servers", len(s.State.Guilds)),
	})

	if err != nil {
		log.Error().AnErr("Failed to update guild count channel", err)
	}

	log.Info().Msgf("Guild deleted %s", gd.Guild.ID)
}
