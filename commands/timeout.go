package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	services.Commands[timeoutCmd.Name] = &services.Command{
		ApplicationCommand: timeoutCmd,
		Handler:            handleTimeout,
	}
}

var timeoutCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "timeout",
	Description: "Timeout a user",
	Options: []*discordgo.ApplicationCommandOption{
		timeoutAddCmd,
		timeoutRemoveCmd,
	},
	DefaultMemberPermissions: &lib.Permissions.ModerateMembers,
}

func handleTimeout(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if !utils.CheckPerms(i.Member, lib.Permissions.ModerateMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "add":
		return handleTimeoutAdd(s, i)
	case "remove":
		return handleTimeoutRemove(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exits"), true)
}
