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
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "setup":
		if !utils.CheckPerms(i.Member, administrator) {
			return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
		}
		return handleAppealsSetup(s, i)
	case "queue":
		if !utils.CheckPerms(i.Member, moderateMembers) {
			return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
		}
		return handleAppealsQueue(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
