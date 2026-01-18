package commands

import (
	"database/sql"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
)

func init() {
	services.Commands[unbanCmd.Name] = &services.Command{
		ApplicationCommand: unbanCmd,
		Handler:            handleUnban,
	}
}

var unbanCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "unban",
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
	if !utils.CheckPerms(i.Member, banMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	userToUnban := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"

	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToUnban == nil {
		return components.EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// make sure the user isn't kicking themselves
	if userToUnban.ID == moderator.ID {
		return components.EmbedResponse(components.ErrorEmbed("You can't unban yourself."), true)
	}
	// make sure the user isn't kicking the bot
	if userToUnban.ID == s.State.User.ID {
		return components.EmbedResponse(components.ErrorEmbed("You can't unban me using this command."), true)
	}

	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID:          id,
			Type:        3,
			Reason:      reason,
			UserID:      userToUnban.ID,
			ModeratorID: moderator.ID,
			GuildID:     guild.ID,
		}

		invites, err := s.GuildInvites(guild.ID)
		if err != nil {
			log.Error().AnErr("Failed to generate invite link", err)
			services.CaptureError(err)
		}
		inviteLink := "https://discord.gg/" + invites[0].Code
		dmError := ""
		dmEmbed := components.NewEmbed().
			SetDescription("You have been unbanned from **"+guild.Name+"** for ```"+reason+"```\n\nYou can rejoin the server using [this link]("+inviteLink+").").
			SetColor("Green").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		// unban the user
		err = s.GuildBanDelete(guild.ID, userToUnban.ID)
		if err != nil {
			log.Error().AnErr("Failed to unban user", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to unban user.\n```" + err.Error() + "```")},
			})
			return
		}

		// attempt to send the user a DM
		err = utils.DMUserEmbed(userToUnban.ID, dmEmbed, s)
		if err != nil {
			dmError = "\n\n-# *User has DMs disabled.*"
		}

		// look in the appeals table for an appeal with the user ID and guild ID
		appeals, err := storage.FindAppealsByUserID(userToUnban.ID, guild.ID)
		if err != nil {
			log.Error().AnErr("Failed to find appeals", err)
			services.CaptureError(err)
		}
		if len(appeals) > 0 {
			// look for any pending or rejected appeals
			for _, appeal := range appeals {
				if appeal.Status == 0 || appeal.Status == 2 {
					// update the appeal status to accepted
					err = storage.UpdateAppealStatus(appeal.ID, 1, moderator.ID)
					if err != nil {
						log.Error().AnErr("Failed to update appeal status", err)
						services.CaptureError(err)
					}
				}
			}
		}

		// send the response
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has been unbanned for `%s`%s", userToUnban.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s unbanned %s", moderator.Username, userToUnban.Username), userToUnban.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed
		msg, _ := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

		if msg != nil {
			caseData.ContextURL = sql.NullString{String: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, msg.ID), Valid: true}
		}

		// ensure case saved after message URL
		err = storage.CreateCase(caseData)
		if err != nil {
			log.Error().AnErr("Failed to create case", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to save case.\n```" + err.Error() + "```")},
			})
			return
		}
	}()

	return components.LoadingResponse()
}
