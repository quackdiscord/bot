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
		{
			Type:        discordgo.ApplicationCommandOptionBoolean,
			Name:        "appeal",
			Description: "Should the user be able to appeal this ban?",
			Required:    false,
		},
	},
	DefaultMemberPermissions: &banMembers,
}

func handleBan(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// check if the user has the required permissions
	if !utils.CheckPerms(i.Member, banMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	data := i.ApplicationCommandData()
	var userToBan *discordgo.User
	reason := "No reason provided"
	appeal := true // default allow appeals unless specified otherwise
	for _, opt := range data.Options {
		switch opt.Name {
		case "user":
			userToBan = opt.UserValue(s)
		case "reason":
			reason = opt.StringValue()
		case "appeal":
			appeal = opt.BoolValue()
		}
	}

	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToBan == nil {
		return components.EmbedResponse(components.ErrorEmbed("User not found."), true)
	}
	// reason is already populated from options if provided

	// make sure the user isn't banning themselves
	if userToBan.ID == moderator.ID {
		return components.EmbedResponse(components.ErrorEmbed("You can't ban yourself."), true)
	}
	// make sure the user isn't banning the bot
	if userToBan.ID == s.State.User.ID {
		return components.EmbedResponse(components.ErrorEmbed("You can't ban me using this command."), true)
	}

	go func() {
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
		dmDescription := "🚨 You have been banned from **" + guild.Name + "** for ```" + reason + "```"
		dmEmbed := components.NewEmbed().
			SetDescription(dmDescription).
			SetColor("Red").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().MessageEmbed

		// attempt to DM the user
		// If appeals are configured, include appeal button in DM
		dmComponents := []discordgo.MessageComponent{}
		if asettings, _ := storage.FindAppealSettingsByGuildID(i.GuildID); asettings != nil && appeal {
			dmComponents = []discordgo.MessageComponent{
				discordgo.ActionsRow{Components: []discordgo.MessageComponent{
					discordgo.Button{Label: "Appeal this ban", Style: discordgo.PrimaryButton, CustomID: "appeal-open:" + i.GuildID},
				}},
			}
			dmDescription += "\n\nThis ban can be appealed.\n\n**If the button below doesn't work, please click [here](https://discord.com/oauth2/authorize?client_id=" + s.State.User.ID + ") and select \"Add to My Apps\", then try again.**"
			dmEmbed.Description = dmDescription
		}
		log.Debug().Msgf("[ban] attempting to DM user with appeal button; guild: %s user: %s", i.GuildID, userToBan.ID)
		dmChannel, derr := s.UserChannelCreate(userToBan.ID)
		var err error
		if derr == nil {
			log.Debug().Msgf("[ban] DM channel created: %s", dmChannel.ID)
			_, err = s.ChannelMessageSendComplex(dmChannel.ID, &discordgo.MessageSend{
				Embeds:     []*discordgo.MessageEmbed{dmEmbed},
				Components: dmComponents,
			})
		} else {
			log.Debug().Msgf("[ban] failed to create DM channel: %s", derr.Error())
			err = derr
		}
		if err != nil {
			dmError = "\n\n-# *User has DMs disabled. User cannot appeal this ban.*"
			log.Debug().Msgf("[ban] failed to send DM: %s", err.Error())
		}

		// ban the user
		err = s.GuildBanCreateWithReason(i.GuildID, userToBan.ID, reason, 1)
		if err != nil {
			log.Error().AnErr("Failed to ban user", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to ban user.\n```" + err.Error() + "```")},
			})
			return
		}

		if !appeal {
			dmError = "\n\n-# *User cannot appeal this ban.*"
		}

		// create the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<:ban:1165590688554033183> <@%s> has been banned for `%s`%s", userToBan.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s banned %s", moderator.Username, userToBan.Username), userToBan.AvatarURL("")).
			SetFooter("Case ID: " + id).
			SetTimestamp().
			MessageEmbed

		// edit the original response and capture the message for URL
		msg, _ := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

		// attach context URL and save the case
		if msg != nil {
			caseData.ContextURL = sql.NullString{String: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, msg.ID), Valid: true}
		}

		// save the case
		err = storage.CreateCase(caseData)
		if err != nil {
			log.Error().AnErr("Failed to save case", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to save case.\n```" + err.Error() + "```")},
			})
			return
		}

	}()

	return components.LoadingResponse()
}
