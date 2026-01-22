package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/owner"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onMessageCreate)
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// access the message cache
	if m.Message != nil {
		services.MsgCache.AddMessage(m.Message)
	}

	isHoneypot := false
	if m.Author != nil && m.Author.ID != s.State.User.ID {
		// store the message in redis (this will check if the message is in a ticket automatically)
		storage.StoreTicketMessage(m.ChannelID, m.Content, m.Author.Username)
		isHoneypot = storage.IsHoneypotChannel(m.ChannelID)
	}

	if isHoneypot {
		honeypot, err := storage.GetHoneypot(m.ChannelID)
		if err != nil {
			log.Error().AnErr("Failed to get honeypot", err)
			services.CaptureError(err)
			return
		}
		HandleHoneypotMessage(s, m, honeypot)
		return
	}

	owner.Handle(s, m)
}
