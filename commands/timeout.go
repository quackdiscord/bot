package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[timeoutCmd.Name] = &services.Command{
		ApplicationCommand: timeoutCmd,
		Handler:            handleTimeout,
	}
}

var timeoutCmd = &discordgo.ApplicationCommand{
	Type: discordgo.ChatApplicationCommand,
	Name: "timeout",
	Description: "Timeout a user",
	Options: []*discordgo.ApplicationCommandOption{
		timeoutAddCmd,
		timeoutRemoveCmd,
	},
	DefaultMemberPermissions: &moderateMembers,
}

func handleTimeout(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
		case "add":
			return handleTimeoutAdd(s, i)
		case "remove":
			return handleTimeoutRemove(s, i)
	}

	return ContentResponse("Command does not exits", true)
}
