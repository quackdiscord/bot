package cmds

import (
	"github.com/bwmarrin/discordgo"
)

func init() {
	Commands[cmdBan.Name] = &Command{
		ApplicationCommand: cmdBan,
		Handler:            handleBan,
	}
}

var cmdBan = &discordgo.ApplicationCommand{
	Type: discordgo.ChatApplicationCommand,
	Name: "ban",
	Description: "Ban a user from the server",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to ban",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the ban",
			Required:    false,
		},
	},
}

func handleBan(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	return ContentResponse("Not done yet", true)
}
