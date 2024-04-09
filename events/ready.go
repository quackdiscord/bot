package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
)

func init() {
	Events = append(Events, onReady)
}

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	logrus.Info("Signed in as " + s.State.User.String())
}