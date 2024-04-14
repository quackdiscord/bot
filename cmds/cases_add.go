package cmds

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

var casesAddCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "add",
	Description: "Add a case to a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to add the case to",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the case",
			Required:    true,
		},
	},
}

func handleCasesAdd(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {

	userToWarn := i.ApplicationCommandData().Options[0].Options[0].UserValue(s)
	reason := i.ApplicationCommandData().Options[0].Options[1].StringValue()
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	// make sure the user is not a bot
	if userToWarn.Bot {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** You can not give a bot a case.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// process the whole thing in a goroutine to avoid blocking the response
	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID: id,
			Type: 0,
			Reason: reason,
			UserID: userToWarn.ID,
			ModeratorID: moderator.ID,
			GuildID: guild.ID,
		}

		dmError := ""
		dmEmbed := components.NewEmbed().
			SetDescription(fmt.Sprintf("You have been warned in **%s** for ```%s```\n> Please discontinue this behavior.", guild.Name, reason)).
			SetColor("Orange").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed

		// attempt to send the user a DM
		dmChannel, err := s.UserChannelCreate(userToWarn.ID)
		if err != nil {
			dmError = "\n\n> User has DMs disabled."
		} else {
			_, err2 := s.ChannelMessageSendEmbed(dmChannel.ID, dmEmbed)
			if err2 != nil {
				dmError = "\n\n> User has DMs disabled."
			}
		}

		// save the case
		err3 := storage.CreateCase(caseData)
		if err3 != nil {
			log.WithError(err3).Error("Failed to create case")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to save case.\n```" + err3.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// form the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<:warn:1165590684837875782> <@%s> has been warned for `%s`%s", userToWarn.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s warned %s", moderator.Username, userToWarn.Username), userToWarn.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
