package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/cmds"
)

func init() {
	Events = append(Events, onInteractionCreate)
}

func onInteractionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		cmds.OnInteraction(s, i)
	}
	// case discordgo.InteractionMessageComponent:
	// 	components.OnInteraction(s, i)
	// }
}
