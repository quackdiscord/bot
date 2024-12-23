package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[lockdownCmd.Name] = &services.Command{
		ApplicationCommand: lockdownCmd,
		Handler:            handleLockdown,
	}
}

var lockdownCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "lockdown",
	Description:              "Lockdown a channel to prevent new messages",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to lockdown",
			Required:    true,
		},
	},
}

func handleLockdown(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	return EmbedResponse(components.ErrorEmbed("This command is not yet implemented."), true)
}
