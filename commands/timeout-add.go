package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
)

var timeoutAddCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "add",
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
			Name:        "time",
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

	if i.Member == nil {
		return EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToTime == nil {
		return EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	if len(i.ApplicationCommandData().Options[0].Options) > 2 {
		reason = i.ApplicationCommandData().Options[0].Options[2].StringValue()
	}

	// make sure the user isn't timing themselves out
	if userToTime.ID == moderator.ID {
		return EmbedResponse(components.ErrorEmbed("You cannot time out yourself."), true)
	}

	// make sure the user isn't timing out a bot
	if userToTime.Bot {
		return EmbedResponse(components.ErrorEmbed("You cannot time out a bot."), true)
	}

	// create the case
	id, _ := lib.GenID()
	caseData := &structs.Case{
		ID:          id,
		Type:        4,
		Reason:      reason,
		UserID:      userToTime.ID,
		ModeratorID: moderator.ID,
		GuildID:     guild.ID,
	}

	// create the timeout
	until, _ := lib.ParseTime(lengthOfTime)
	err := s.GuildMemberTimeout(guild.ID, userToTime.ID, &until)
	if err != nil {
		log.Error().AnErr("Failed to time out user", err)
		return EmbedResponse(components.ErrorEmbed("Failed to time out user.\n```"+err.Error()+"```"), true)
	}

	// save the case
	err = storage.CreateCase(caseData)
	if err != nil {
		log.Error().AnErr("Failed to save case", err)
		return EmbedResponse(components.ErrorEmbed("Failed to save case.\n```"+err.Error()+"```"), true)
	}

	// dms
	dmError := ""
	dmEmbed := components.NewEmbed().
		SetDescription(fmt.Sprintf("You have been timed out in **%s** for ```%s```\n> Timeout will expire <t:%d:R>", guild.Name, reason, until.Unix())).
		SetColor("Yellow").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter(fmt.Sprintf("Case ID: %s", id)).
		SetTimestamp().MessageEmbed

	err = utils.DMUserEmbed(userToTime.ID, dmEmbed, s)
	if err != nil {
		dmError = "\n\n> User has DMs disabled."
	}

	// create the embeds
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> has been timed out for `%s`. Timed out for `%s`. Expires <t:%d:R>%s", userToTime.ID, reason, lengthOfTime, until.Unix(), dmError)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s timed out %s", moderator.Username, userToTime.Username), userToTime.AvatarURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().
		MessageEmbed

	return EmbedResponse(embed, false)

}
