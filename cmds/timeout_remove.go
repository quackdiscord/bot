package cmds

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
)

var timeoutRemoveCmd = &discordgo.ApplicationCommandOption{
	Type: discordgo.ApplicationCommandOptionSubCommand,
	Name: "remove",
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

	if userToUntime == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** User not found.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// remove the timeout from the user
	go func() {
		err := s.GuildMemberTimeout(guild.ID, userToUntime.ID, nil)
		if err != nil {
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to untime out user.\n```" + err.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// create the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has been untimed out.", userToUntime.ID)).
			SetColor("Main").
			SetAuthor("Untimed Out " + userToUntime.Username, userToUntime.AvatarURL("")).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}
