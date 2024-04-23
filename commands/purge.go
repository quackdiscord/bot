package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[purgeCmd.Name] = &services.Command{
		ApplicationCommand: purgeCmd,
		Handler:            handlePurge,
	}
}

var purgeCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "purge",
	Description:              "Purge messages from the server",
	DefaultMemberPermissions: &moderateMembers,
	Options: 				  []*discordgo.ApplicationCommandOption{
		purgeAllCmd,
		purgeUserCmd,
		purgeQuackCmd,
	},
}

func handlePurge(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "all":
		return handlePurgeAll(s, i)
	case "user":
		return handlePurgeUser(s, i)
	case "quack":
		return handlePurgeQuack(s, i)
	}

	return ContentResponse("Command does not exist", true)
}
