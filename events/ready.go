package events

import (
	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

func init() {
	Events = append(Events, onReady)
}

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Info("Signed in as " + s.State.User.String())
}
