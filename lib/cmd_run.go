package lib

import (
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

func CmdRun(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	log.WithFields(log.Fields{
		"command": data.Name,
		"guild":   i.GuildID,
		"user":    i.Member.User.ID,
	}).Info("Command executed")
	return
}
