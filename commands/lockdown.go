package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
)

func init() {
	services.Commands[lockdownCmd.Name] = &services.Command{
		ApplicationCommand: lockdownCmd,
		Handler:            handleLockdown,
	}
}

var lockdownCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "lockdown",
	Description:              "Lockdown a channel to prevent new messages",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to lockdown",
			Required:    true,
		},
	},
}

func handleLockdown(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if !utils.CheckPerms(i.Member, moderateMembers) {
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	c := i.ApplicationCommandData().Options[0].ChannelValue(s)
	if c == nil {
		return EmbedResponse(components.ErrorEmbed("Channel not found."), true)
	}

	if c.Type == discordgo.ChannelTypeGuildText {
		overwrites := c.PermissionOverwrites
		// Find if there's an existing overwrite for the guild role
		var foundGuild bool
		for i, overwrite := range overwrites {
			if overwrite.ID == c.GuildID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
				// Modify existing overwrite to add the new denied permissions
				overwrites[i].Deny |= discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads
				foundGuild = true
				break
			}
		}

		// If no existing overwrite was found for the guild role, add a new one
		if !foundGuild {
			overwrites = append(overwrites, &discordgo.PermissionOverwrite{
				ID:   c.GuildID,
				Type: discordgo.PermissionOverwriteTypeRole,
				Deny: discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads,
			})
		}

		// Update the channel with all permission overwrites
		_, err := s.ChannelEdit(c.ID, &discordgo.ChannelEdit{
			PermissionOverwrites: overwrites,
		})
		if err != nil {
			log.Error().AnErr("Failed to update channel permissions", err)
			services.CaptureError(err)
			return EmbedResponse(components.ErrorEmbed("Failed to update channel permissions."), true)
		}

		// send a message in the channel
		embed := components.NewEmbed().
			SetTitle("This channel has been locked.").
			SetDescription("Please wait for a moderator to unlock this channel.").
			SetColor("Main").
			SetTimestamp().
			MessageEmbed

		_, err = s.ChannelMessageSendEmbed(c.ID, embed)
		if err != nil {
			return EmbedResponse(components.ErrorEmbed("Failed to send message to channel."), true)
		}

		embed = components.NewEmbed().
			SetDescription(fmt.Sprintf("🔒 <#%s> has been locked down.", c.ID)).
			SetColor("Main").
			MessageEmbed

		return EmbedResponse(embed, false)
	}

	return EmbedResponse(components.ErrorEmbed("This channel type is not supported."), true)
}
