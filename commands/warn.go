package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[warnCmd.Name] = &services.Command{
		ApplicationCommand: warnCmd,
		Handler:            handleCasesAdd, // this is just an alias for /cases add
	}
}

var warnCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "warn",
	Description:              "Alias for /cases add",
	DefaultMemberPermissions: &lib.Permissions.ModerateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to warn",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the warning",
			Required:    true,
		},
	},
}
