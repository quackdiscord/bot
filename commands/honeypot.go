package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[honeypotCmd.Name] = &services.Command{
		ApplicationCommand: honeypotCmd,
		Handler:            handleHoneypot,
	}
}

var honeypotCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "honeypot",
	Description:              "Honeypot commands",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		honeypotCreateCmd,
		// honeyPotUpdateCmd,
	},
}

func handleHoneypot(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "create":
		return handleHoneypotCreate(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
