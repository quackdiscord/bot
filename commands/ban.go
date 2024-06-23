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
	services.Commands[banCmd.Name] = &services.Command{
		ApplicationCommand: banCmd,
		Handler:            handleBan,
	}
}

var banCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "ban",
	Description: "Ban a user from the server",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to ban",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "reason",
			Description: "The reason for the ban",
			Required:    false,
		},
	},
	DefaultMemberPermissions: &banMembers,
}

func handleBan(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {

	userToBan := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToBan == nil {
		return EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// make sure the user isn't banning themselves
	if userToBan.ID == moderator.ID {
		return EmbedResponse(components.ErrorEmbed("You can't ban yourself."), true)
	}
	// make sure the user isn't banning the bot
	if userToBan.ID == s.State.User.ID {
		return EmbedResponse(components.ErrorEmbed("You can't ban me using this command."), true)
	}
	// create the case
	id, _ := lib.GenID()
	caseData := &structs.Case{
		ID:          id,
		Type:        1,
		Reason:      reason,
		UserID:      userToBan.ID,
		GuildID:     i.GuildID,
		ModeratorID: moderator.ID,
	}

	// set up embeds
	dmError := ""
	dmEmbed := components.NewEmbed().
		SetDescription("🚨 You have been banned from **"+guild.Name+"** for ```"+reason+"```").
		SetColor("Red").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().MessageEmbed

	// attempt to DM the user
	dmChannel, err := s.UserChannelCreate(userToBan.ID)
	if err != nil {
		dmError = "\n\n> User has DMs disabled."
	} else {
		_, err = s.ChannelMessageSendEmbed(dmChannel.ID, dmEmbed)
		if err != nil {
			dmError = "\n\n> User has DMs disabled."
		}
	}

	// ban the user
	err = s.GuildBanCreateWithReason(i.GuildID, userToBan.ID, reason, 1)
	if err != nil {
		log.WithError(err).Error("Failed to ban user")
		return EmbedResponse(components.ErrorEmbed("Failed to ban user.\n```"+err.Error()+"```"), true)
	}

	// save the case
	err = storage.CreateCase(caseData)
	if err != nil {
		log.WithError(err).Error("Failed to save case")
		return EmbedResponse(components.ErrorEmbed("Failed to save case.\n```"+err.Error()+"```"), true)
	}

	// create the embed
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<:ban:1165590688554033183> <@%s> has been banned for `%s`%s", userToBan.ID, reason, dmError)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s banned %s", moderator.Username, userToBan.Username), userToBan.AvatarURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().
		MessageEmbed

	return EmbedResponse(embed, false)
}
