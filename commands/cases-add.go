package commands

import (
	"database/sql"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
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

	if i.Member == nil {
		return EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	// make sure the user is not a bot
	if userToWarn.Bot {
		return EmbedResponse(components.ErrorEmbed("You can not give a bot a case."), true)
	}

	go func() {
		// create the case
		id, _ := lib.GenID()
		caseData := &structs.Case{
			ID:          id,
			Type:        0,
			Reason:      reason,
			UserID:      userToWarn.ID,
			ModeratorID: moderator.ID,
			GuildID:     guild.ID,
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
		err := utils.DMUserEmbed(userToWarn.ID, dmEmbed, s)
		if err != nil {
			dmError = "\n\n-# *User has DMs disabled.*"
		}

		// form the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<:warn:1165590684837875782> <@%s> has been warned for `%s`%s", userToWarn.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s warned %s", moderator.Username, userToWarn.Username), userToWarn.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		// edit the original response and capture message for URL
		msg, _ := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
		if msg != nil {
			caseData.ContextURL = sql.NullString{String: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, msg.ID), Valid: true}
		}

		// save the case after capturing URL
		err = storage.CreateCase(caseData)
		if err != nil {
			log.Error().AnErr("Failed to create case", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to save case.\n```" + err.Error() + "```")}})
			return
		}
	}()

	return LoadingResponse()
}
