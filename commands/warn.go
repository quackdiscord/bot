package commands

import (
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
	services.Commands[warnCmd.Name] = &services.Command{
		ApplicationCommand: warnCmd,
		Handler:            handleWarn,
	}
}

// cases add @user reason
// cases remove id :id
// cases remove user @user
// cases remove latest
// cases view user @user
// cases view latest
// cases view id :id

var warnCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "warn",
	Description:              "Alias for /cases add",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to warn",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the warning",
			Required:    true,
		},
	},
}

func handleWarn(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if !utils.CheckPerms(i.Member, moderateMembers) {
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	userToWarn := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := i.ApplicationCommandData().Options[1].StringValue()

	if i.Member == nil {
		return EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	// make sure the user is not a bot
	if userToWarn.Bot {
		return EmbedResponse(components.ErrorEmbed("You can not give a bot a case."), true)
	}

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

	// save the case
	err = storage.CreateCase(caseData)
	if err != nil {
		log.Error().AnErr("Failed to create case", err)
		return EmbedResponse(components.ErrorEmbed("Failed to save case.\n```"+err.Error()+"```"), true)
	}

	// form the embed
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<:warn:1165590684837875782> <@%s> has been warned for `%s`%s", userToWarn.ID, reason, dmError)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s warned %s", moderator.Username, userToWarn.Username), userToWarn.AvatarURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().MessageEmbed

	return EmbedResponse(embed, false)
}
