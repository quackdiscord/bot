package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
)

var timeoutRemoveCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "remove",
	Description: "Remove a timeout from a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to untimeout",
			Required:    true,
		},
	},
}

func handleTimeoutRemove(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	userToUntime := i.ApplicationCommandData().Options[0].Options[0].UserValue(s)
	guild, _ := s.Guild(i.GuildID)

	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User

	if userToUntime == nil {
		return components.EmbedResponse(components.ErrorEmbed("User not found."), true)
	}

	// remove the timeout from the user
	err := s.GuildMemberTimeout(guild.ID, userToUntime.ID, nil)
	if err != nil {
		return components.EmbedResponse(components.ErrorEmbed("Failed to untime out user.\n```"+err.Error()+"```"), true)
	}

	// create the embed
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> has been untimed out.", userToUntime.ID)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s untimed out %s", moderator.Username, userToUntime.Username), userToUntime.AvatarURL("")).
		SetTimestamp().
		MessageEmbed

	return components.EmbedResponse(embed, false)
}
