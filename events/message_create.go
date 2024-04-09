package events

import (
	"github.com/bwmarrin/discordgo"
)

func init() {
	Events = append(Events, messageCreate)
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// ignore all messages created by the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// todo
}