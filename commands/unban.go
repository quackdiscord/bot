package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

func init() {
	services.Commands[unbanCmd.Name] = &services.Command{
		ApplicationCommand: unbanCmd,
		Handler:            handleUnban,
	}
}

var unbanCmd = &discordgo.ApplicationCommand{
	Type: discordgo.ChatApplicationCommand,
	Name: "unban",
	Description: "Unban a user from the server",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to unban",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the unban",
			Required:    false,
		},
	},
	DefaultMemberPermissions: &banMembers,
}

func handleUnban(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	userToUnban := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToUnban == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** User not found.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}
	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// make sure the user isn't kicking themselves
	if userToUnban.ID == moderator.ID {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** You can't unban yourself.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}
	// make sure the user isn't kicking the bot
	if userToUnban.ID == s.State.User.ID {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** You can't unban me using this command.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID: id,
			Type: 3,
			Reason: reason,
			UserID: userToUnban.ID,
			ModeratorID: moderator.ID,
			GuildID: guild.ID,
		}

		dmError := ""
		dmEmbed := components.NewEmbed().
			SetDescription("You have been unbanned from **" + guild.Name + "** for ```" + reason + "```").
			SetColor("Green").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		// unban the user
		err3 := s.GuildBanDelete(guild.ID, userToUnban.ID)
		if err3 != nil {
			log.WithError(err3).Error("Failed to unban user")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to unban user.\n```" + err3.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// attempt to send the user a DM
		dmChannel, err := s.UserChannelCreate(userToUnban.ID)
		if err != nil {
			dmError = "\n\n> User has DMs disabled."
		} else {
			_, err2 := s.ChannelMessageSendEmbed(dmChannel.ID, dmEmbed)
			if err2 != nil {
				dmError = "\n\n> User has DMs disabled."
			}
		}

		// save the case
		err4 := storage.CreateCase(caseData)
		if err4 != nil {
			log.WithError(err4).Error("Failed to create case")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to save case.\n```" + err4.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// send the response
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has been unbanned for `%s`%s", userToUnban.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s unbanned %s", moderator.Username, userToUnban.Username), userToUnban.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
