package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/storage"
)

func init() {
	Events = append(Events, onMessageCreate)
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}
	// store the message in redis (this will check if the message is in a ticket automatically)
	storage.StoreTicketMessage(m.ChannelID, m.Content, m.Author.Username)
}
