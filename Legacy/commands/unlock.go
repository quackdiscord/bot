package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
)

func init() {
	services.Commands[unlockCmd.Name] = &services.Command{
		ApplicationCommand: unlockCmd,
		Handler:            handleUnlock,
	}
}

var unlockCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "unlock",
	Description:              "Unlock a channel to allow new messages",
	DefaultMemberPermissions: &lib.Permissions.ModerateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to unlock",
			Required:    true,
		},
	},
}

func handleUnlock(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	if !utils.CheckPerms(i.Member, lib.Permissions.ModerateMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	c := i.ApplicationCommandData().Options[0].ChannelValue(s)
	if c == nil {
		return components.EmbedResponse(components.ErrorEmbed("Channel not found."), true)
	}

	if c.Type == discordgo.ChannelTypeGuildText {
		overwrites := c.PermissionOverwrites
		// Find the guild role overwrite
		for i, overwrite := range overwrites {
			if overwrite.ID == c.GuildID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
				// Remove the send message permissions from Deny
				overwrites[i].Deny &^= lib.Permissions.SendMessages | lib.Permissions.SendMessagesInThreads

				// If this overwrite now has no permissions, remove it completely
				if overwrites[i].Allow == 0 && overwrites[i].Deny == 0 {
					// Remove this overwrite by taking everything before and after it
					overwrites = append(overwrites[:i], overwrites[i+1:]...)
				}
				break
			}
		}

		// Update the channel with modified permission overwrites
		_, err := s.ChannelEdit(c.ID, &discordgo.ChannelEdit{
			PermissionOverwrites: overwrites,
		})
		if err != nil {
			log.Error().AnErr("Failed to update channel permissions", err)
			services.CaptureError(err)
			return components.EmbedResponse(components.ErrorEmbed("Failed to update channel permissions."), true)
		}

		// send a message in the channel
		embed := components.NewEmbed().
			SetTitle("This channel has been unlocked.").
			SetDescription("You may now send messages in this channel.").
			SetColor("Main").
			SetTimestamp().
			MessageEmbed

		_, err = s.ChannelMessageSendEmbed(c.ID, embed)
		if err != nil {
			return components.EmbedResponse(components.ErrorEmbed("Failed to send message to channel."), true)
		}

		embed = components.NewEmbed().
			SetDescription(fmt.Sprintf("🔓 <#%s> has been unlocked.", c.ID)).
			SetColor("Main").
			MessageEmbed

		return components.EmbedResponse(embed, false)
	}

	return components.EmbedResponse(components.ErrorEmbed("This channel type is not supported."), true)
}
