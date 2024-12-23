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
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	// defer the response
	LoadingResponse()

	userToUnban := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"

	if i.Member == nil {
		return EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToUnban == nil {
		return EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// make sure the user isn't kicking themselves
	if userToUnban.ID == moderator.ID {
		return EmbedResponse(components.ErrorEmbed("You can't unban yourself."), true)
	}
	// make sure the user isn't kicking the bot
	if userToUnban.ID == s.State.User.ID {
		return EmbedResponse(components.ErrorEmbed("You can't unban me using this command."), true)
	}

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

	dmError := ""
	dmEmbed := components.NewEmbed().
		SetDescription("You have been unbanned from **"+guild.Name+"** for ```"+reason+"```").
		SetColor("Green").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().MessageEmbed

	// unban the user
	err := s.GuildBanDelete(guild.ID, userToUnban.ID)
	if err != nil {
		log.Error().AnErr("Failed to unban user", err)
		return EmbedResponse(components.ErrorEmbed("Failed to unban user.\n```"+err.Error()+"```"), true)
	}

	// attempt to send the user a DM
	dmChannel, err := s.UserChannelCreate(userToUnban.ID)
	if err != nil {
		dmError = "\n\n> User has DMs disabled."
	} else {
		_, err = s.ChannelMessageSendEmbed(dmChannel.ID, dmEmbed)
		if err != nil {
			dmError = "\n\n> User has DMs disabled."
		}
	}

	// save the case
	err = storage.CreateCase(caseData)
	if err != nil {
		log.Error().AnErr("Failed to create case", err)
		return EmbedResponse(components.ErrorEmbed("Failed to save case.\n```"+err.Error()+"```"), true)
	}

	// send the response
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> has been unbanned for `%s`%s", userToUnban.ID, reason, dmError)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s unbanned %s", moderator.Username, userToUnban.Username), userToUnban.AvatarURL("")).
		SetFooter("Case ID: " + id).
		SetTimestamp().
		MessageEmbed

	return EmbedResponse(embed, false)
}
