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
	services.Commands[kickCmd.Name] = &services.Command{
		ApplicationCommand: kickCmd,
		Handler:            handleKick,
	}
}

var kickCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "kick",
	Description: "Kick a user from the server",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to kick",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the kick",
			Required:    false,
		},
	},
	DefaultMemberPermissions: &kickMembers,
}

func handleKick(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	userToKick := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToKick == nil {
		return EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// make sure the user isn't kicking themselves
	if userToKick.ID == moderator.ID {
		return EmbedResponse(components.ErrorEmbed("You can't kick yourself."), true)
	}
	// make sure the user isn't kicking the bot
	if userToKick.ID == s.State.User.ID {
		return EmbedResponse(components.ErrorEmbed("You can't kick me using this command."), true)
	}

	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID:          id,
			Type:        2,
			Reason:      reason,
			UserID:      userToKick.ID,
			ModeratorID: moderator.ID,
			GuildID:     guild.ID,
		}

		// set up embeds
		dmError := ""
		dmEmbed := components.NewEmbed().
			SetDescription("You have been kicked from **"+guild.Name+"** for ```"+reason+"```").
			SetColor("Error").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		// attempt to DM the user
		dmChannel, err := s.UserChannelCreate(userToKick.ID)
		if err != nil {
			dmError = "\n\n> User has DMs disabled."
		} else {
			_, err2 := s.ChannelMessageSendEmbed(dmChannel.ID, dmEmbed)
			if err2 != nil {
				dmError = "\n\n> User has DMs disabled."
			}
		}

		// kick the user
		err3 := s.GuildMemberDeleteWithReason(guild.ID, userToKick.ID, reason)
		if err3 != nil {
			log.WithError(err3).Error("Failed to kick user")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to kick user.\n```" + err3.Error() + "```")},
			})
			return
		}

		// save the case
		err4 := storage.CreateCase(caseData)
		if err4 != nil {
			log.WithError(err4).Error("Failed to create case")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to save case.\n```" + err4.Error() + "```")},
			})
			return
		}

		// create the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("👋 <@%s> has been kicked for `%s`%s", userToKick.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s kicked out %s", moderator.Username, userToKick.Username), userToKick.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
