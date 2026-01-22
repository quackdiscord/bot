package commands

import (
	"database/sql"
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/actions"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
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
	DefaultMemberPermissions: &lib.Permissions.BanMembers,
}

func handleBan(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	data := i.ApplicationCommandData()
	userToBan := data.Options[0].UserValue(s)
	reason := "No reason provided"
	if len(data.Options) > 1 {
		reason = data.Options[1].StringValue()
	}
	appeal := true
	if len(data.Options) > 2 {
		appeal = data.Options[2].BoolValue()
	}

	// check if the user has the required permissions
	if !utils.CheckPerms(i.Member, lib.Permissions.BanMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if userToBan == nil {
		return components.EmbedResponse(components.ErrorEmbed("User not found."), true)
	}

	go func() {
		result := actions.Ban(s, actions.BanParams{
			GuildID:     guild.ID,
			UserID:      userToBan.ID,
			ModeratorID: moderator.ID,
			Reason:      reason,
			AllowAppeal: appeal,
		})

		if result.Error != nil {
			log.Error().AnErr("Failed to ban user", result.Error)
			services.CaptureError(result.Error)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to ban user.\n```" + result.Error.Error() + "```")},
			})
			return
		}

		dmError := ""
		if result.DMFailed {
			dmError = "\n\n-# *User has DMs disabled.*"
			if !appeal {
				dmError += "*They cannot appeal.*"
			}
		} else if !appeal && !result.DMFailed {
			dmError += "\n\n-# *User cannot appeal.*"
		}

		// create the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<:ban:1165590688554033183> <@%s> has been banned for `%s`%s", userToBan.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s banned %s", moderator.Username, userToBan.Username), userToBan.AvatarURL("")).
			SetFooter("Case ID: " + result.Case.ID).
			SetTimestamp().
			MessageEmbed

		// edit the original response and capture the message for URL
		msg, _ := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

		// attach context URL and update the case in db
		if msg != nil {
			result.Case.ContextURL = sql.NullString{String: fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, i.ChannelID, msg.ID), Valid: true}
		}

		if err := storage.UpdateCase(result.Case); err != nil {
			log.Error().AnErr("Failed to update case", err)
			services.CaptureError(err)
		}

	}()

	return components.LoadingResponse()
}
