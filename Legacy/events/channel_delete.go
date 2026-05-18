package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onChannelDelete)
}

func onChannelDelete(s *discordgo.Session, c *discordgo.ChannelDelete) {
	services.EQ.Enqueue(services.Event{
		Type:    "channel_delete",
		Data:    c,
		GuildID: c.GuildID,
	})
}

func channelDeleteHandler(e services.Event) error {
	// for now all this needs to do is check if the channel is a honeypot
	if storage.IsHoneypotChannel(e.Data.(*discordgo.ChannelDelete).Channel.ID) {
		// remove the channel from the database
		err := storage.DeleteHoneypot(e.Data.(*discordgo.ChannelDelete).Channel.ID, e.GuildID)
		if err != nil {
			return err
		}
		log.Info().Msgf("Honeypot channel deleted %s", e.Data.(*discordgo.ChannelDelete).Channel.ID)
		return nil
	}

	return nil
}
