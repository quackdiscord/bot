package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/commands"
	"github.com/quackdiscord/bot/components"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onInteractionCreate)
}

func onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Debug().Msgf("[events.onInteractionCreate] type: %d", i.Type)
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		log.Debug().Msgf("[events.onInteractionCreate] routing to commands.OnInteraction")
		commands.OnInteraction(s, i)
	case discordgo.InteractionMessageComponent:
		log.Debug().Msgf("[events.onInteractionCreate] routing to components.OnInteraction")
		components.OnInteraction(s, i)
	case discordgo.InteractionModalSubmit:
		log.Debug().Msgf("[events.onInteractionCreate] routing to components.HandleModalSubmit")
		components.HandleModalSubmit(s, i)
	default:
		log.Warn().Msgf("Unknown interaction type %d", i.Type)
	}
}
