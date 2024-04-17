package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

var timeoutAddCmd = &discordgo.ApplicationCommandOption{
	Type: discordgo.ApplicationCommandOptionSubCommand,
	Name: "add",
	Description: "Add a time out to a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to time out",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name: 	  	 "time",
			Description: "The time to time out the user, (e.g. 1d, 1h, 1m, 1s)",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the time out",
			Required:    false,
		},
	},
}

func handleTimeoutAdd(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	userToTime := i.ApplicationCommandData().Options[0].Options[0].UserValue(s)
	lengthOfTime := i.ApplicationCommandData().Options[0].Options[1].StringValue()
	reason := "No reason provided"
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToTime == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** User not found.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}
	if len(i.ApplicationCommandData().Options) > 2 {
		reason = i.ApplicationCommandData().Options[0].Options[2].StringValue()
	}

	// make sure the user isn't timing themselves out
	if userToTime.ID == moderator.ID {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** You cannot time out yourself.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// make sure the user isn't timing out a bot
	if userToTime.Bot {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** You cannot time out a bot.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID: id,
			Type: 4,
			Reason: reason,
			UserID: userToTime.ID,
			ModeratorID: moderator.ID,
			GuildID: guild.ID,
		}

		// create the timeout
		until, _ := lib.ParseTime(lengthOfTime)
		err := s.GuildMemberTimeout(guild.ID, userToTime.ID, &until)
		if err != nil {
			log.WithError(err).Error("Failed to time out user")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to time out user.\n```" + err.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// save the case
		saveErr := storage.CreateCase(caseData)
		if saveErr != nil {
			log.Error("Failed to save case", saveErr)
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to save case.\n```" + saveErr.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// create the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has been timed out for `%s`. Timed out for `%s`.", userToTime.ID, reason, lengthOfTime)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s timed out %s", moderator.Username, userToTime.Username), userToTime.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()


}
