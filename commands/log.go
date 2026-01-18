package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	services.Commands[logCmd.Name] = &services.Command{
		ApplicationCommand: logCmd,
		Handler:            handleLog,
	}
}

// /log channel <type> <channel> - sets the logging channel for a specific log type
// /log disable <type> - disables logging for a specific log type

var logCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "log",
	Description:              "Logging system commands",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		logChannelCmd,
		logDisableCmd,
	},
}

func handleLog(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	if !utils.CheckPerms(i.Member, moderateMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "channel":
		return handleLogChannel(s, i)
	case "disable":
		return handleLogDisable(i) // doesnt need session
	}

	return components.ContentResponse("oh... this is awkward.", true)
}
