package events

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
)

func init() {
	Events = append(Events, onGuildDelete)
}

func onGuildDelete(s *discordgo.Session, gd *discordgo.GuildDelete) {
	// delete the guild
	err := storage.DeleteGuild(gd.Guild.ID)
	if err != nil {
		log.WithError(err).Error("Failed to delete guild")
	}

	// update the guild count channel
	_, err = s.ChannelEdit(config.Bot.GuildCountChannel, &discordgo.ChannelEdit{
		Name: fmt.Sprintf("%d servers", len(s.State.Guilds)),
	})

	if err != nil {
		log.WithError(err).Error("Failed to update guild count channel")
	}

	log.Info("Guild deleted " + gd.Guild.ID)
}
