package cmds

import (
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

func init() {
	Commands[cmdBan.Name] = &Command{
		ApplicationCommand: cmdBan,
		Handler:            handleBan,
	}
}

var cmdBan = &discordgo.ApplicationCommand{
	Type: discordgo.ChatApplicationCommand,
	Name: "ban",
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
	// get the variables
	userToBan := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"
	user := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToBan == nil {
		return ContentResponse("User not found", true)
	}

	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	// validate user
	if userToBan.ID == user.ID {
		embed := components.NewEmbed().SetDescription("**Error:** You can't ban yourself.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	if userToBan.ID == s.State.User.ID {
		embed := components.NewEmbed().SetDescription("**Error:** You can't ban me using this command.").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// create the case
	id, _ := lib.GenID()
	caseData := &structs.Case{
		ID:    			id,
		Type:   		1,
		Reason: 		reason,
		UserID:   		userToBan.ID,
		GuildID: 		i.GuildID,
		ModeratorID:	user.ID,
		CreatedAt: 		time.Now().String(),
	}

	// set up embeds
	embedDesc := "<:ban:1165590688554033183> <@" + userToBan.ID + "> has been banned for `" + reason + "`"
	userEmbedDesc := "🚨 You have been banned from **" +  guild.Name + "** for `" + reason + "`"
	userEmbed := components.NewEmbed().SetDescription(userEmbedDesc).SetColor("Red").SetFooter("Case ID: " + id).SetTimestamp().MessageEmbed

	// attempt to DM the user
	dmChannel, dmErr := s.UserChannelCreate(userToBan.ID)
	if dmErr != nil {
		embedDesc = "<:ban:1165590688554033183> <@" + userToBan.ID + "> has been banned for `" + reason + "`\n\n<:warn:1165590684837875782> User has DMs disabled."
	}
	_, dmErr2 := s.ChannelMessageSendEmbed(dmChannel.ID, userEmbed)
	if dmErr2 != nil {
		embedDesc = "<:ban:1165590688554033183> <@" + userToBan.ID + "> has been banned for `" + reason + "`\n\n<:warn:1165590684837875782> User has DMs disabled."
	}

	// ban the user
	banErr := s.GuildBanCreateWithReason(i.GuildID, userToBan.ID, reason, 1)
	if banErr != nil {
		log.Error("Failed to ban user", banErr)
		embed := components.NewEmbed().SetDescription("**Error:** Failed to ban user.\n```" + banErr.Error() + "```").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// save the case
	saveErr := storage.CreateCase(caseData)
	if saveErr != nil {
		log.Error("Failed to save case", saveErr)
		embed := components.NewEmbed().SetDescription("**Error:** Failed to save case.\n```" + saveErr.Error() + "```").SetColor("Error").MessageEmbed
		return EmbedResponse(embed, true)
	}

	// create the embed
	embed := components.NewEmbed().
		SetDescription(embedDesc).
		SetColor("Main").
		SetFooter("Case ID: " + id).
		SetTimestamp().
		MessageEmbed

	return EmbedResponse(embed, false)
}
