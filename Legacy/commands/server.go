package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[serverCmd.Name] = &services.Command{
		ApplicationCommand: serverCmd,
		Handler:            handleServer,
	}
}

var serverCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "server",
	Description: "Get some information about the server",
	Options: []*discordgo.ApplicationCommandOption{
		serverInfoCmd,
	},
}

func handleServer(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "info":
		return handleServerInfo(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
