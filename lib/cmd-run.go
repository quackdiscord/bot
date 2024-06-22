package lib

import (
	"time"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

func CmdRun(s *discordgo.Session, i *discordgo.InteractionCreate, d time.Duration) {
	data := i.ApplicationCommandData()
	log.WithFields(log.Fields{
		"command": data.Name,
		"guild":   i.GuildID,
		"user":    i.Member.User.ID,
		"took":    d.String(),
	}).Info("Command executed")
}
