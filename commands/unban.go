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
	DefaultMemberPermissions: &lib.Permissions.BanMembers,
}

func handleUnban(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// make sure the user is in a server
	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	// make sure the user has the required permissions
	if !utils.CheckPerms(i.Member, lib.Permissions.BanMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	userToUnban := i.ApplicationCommandData().Options[0].UserValue(s)
	reason := "No reason provided"
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	if len(i.ApplicationCommandData().Options) > 1 {
		reason = i.ApplicationCommandData().Options[1].StringValue()
	}

	go func() {
		result := actions.Unban(s, actions.UnbanParams{
			GuildID:     guild.ID,
			UserID:      userToUnban.ID,
			ModeratorID: moderator.ID,
			Reason:      reason,
		})

		if result.Error != nil {
			log.Error().AnErr("Failed to unban user", result.Error)
			services.CaptureError(result.Error)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to unban user.\n```" + result.Error.Error() + "```")},
			})
			return
		}

		dmError := ""
		if result.DMFailed {
			dmError = "\n\n-# *User has DMs disabled.*"
		}

		// send the response
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has been unbanned for `%s`%s", userToUnban.ID, reason, dmError)).
			SetColor("Main").
			SetAuthor(fmt.Sprintf("%s unbanned %s", moderator.Username, userToUnban.Username), userToUnban.AvatarURL("")).
			SetFooter("Case ID: " + result.Case.ID).
			SetTimestamp().
			MessageEmbed

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
