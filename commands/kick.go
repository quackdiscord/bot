package commands

import (
	"database/sql"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
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
	if !utils.CheckPerms(i.Member, kickMembers) {
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	// we'll defer the response and edit later

	userToKick := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"

	if i.Member == nil {
		return EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

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
		err := utils.DMUserEmbed(userToKick.ID, dmEmbed, s)
		if err != nil {
			dmError = "\n\n-# *User has DMs disabled.*"
		}

		// kick the user
		err = s.GuildMemberDeleteWithReason(guild.ID, userToKick.ID, reason)
		if err != nil {
			log.Error().AnErr("Failed to kick user", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to kick user.\n```" + err.Error() + "```")}})
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

		// edit the original response and capture message for URL
		msg, _ := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
		if msg != nil {
			caseData.ContextURL = sql.NullString{String: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, msg.ID), Valid: true}
		}

		// save the case
		err = storage.CreateCase(caseData)
		if err != nil {
			log.Error().AnErr("Failed to create case", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to save case.\n```" + err.Error() + "```")}})
			return
		}
	}()

	return LoadingResponse()
}
