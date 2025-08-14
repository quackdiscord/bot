package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	services.Commands[appealsCmd.Name] = &services.Command{
		ApplicationCommand: appealsCmd,
		Handler:            handleAppeals,
	}
}

var appealsCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "appeals",
	Description:              "Ban appeals configuration",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		appealsSetupCmd,
		appealsQueueCmd,
	},
}

func handleAppeals(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if !utils.CheckPerms(i.Member, administrator) {
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "setup":
		return handleAppealsSetup(s, i)
	case "queue":
		return handleAppealsQueue(s, i)
	}

	return EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
