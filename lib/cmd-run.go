package lib

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	log "github.com/sirupsen/logrus"
)

func CmdRun(s *discordgo.Session, i *discordgo.InteractionCreate, d time.Duration) {
	data := i.ApplicationCommandData()

	// increment the command run counter
	err := services.Redis.HIncrBy(context.Background(), "seeds:cmds", data.Name, 1).Err()
	if err != nil {
		log.WithError(err).Error("Failed to increment command run counter")
		return
	}

	log.WithFields(log.Fields{
		"command": data.Name,
		"guild":   i.GuildID,
		"user":    i.Member.User.ID,
		"took":    d.String(),
	}).Info("Command executed")
}
