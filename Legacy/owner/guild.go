package owner

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

func init() {
	Commands["guild"] = &Command{
		Name:    "guild",
		Handler: guild,
	}
}

func guild(s *discordgo.Session, m *discordgo.MessageCreate) {
	var guild *discordgo.Guild
	// get the guild id by splitting the message at the space
	if !strings.Contains(m.Content, " ") {
		s.ChannelMessageSend(m.ChannelID, "Please provide a guild ID")
		return
	}

	guildID := strings.Split(m.Content, " ")[1]

	if guildID == "" {
		s.ChannelMessageSend(m.ChannelID, "Please provide a guild ID")
		return
	}

	// get the guild
	guild, err := s.GuildWithCounts(guildID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get guild")
		services.CaptureError(err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to get guild %s ```%s```", guildID, err))
		return
	}

	desc := fmt.Sprintf(
		"**Description:**\n<:text:1229343822337802271>%s\n\n**Numbers**\n<:text2:1229344477131309136>**Members:** %d / %d\n<:text2:1229344477131309136>**Boost Tier:** %d\n<:text:1229343822337802271>**Emojis:** %d\n\n**Owner:**\n<:text:1229343822337802271> <:owner:1230302954683367436> <@%s>",
		guild.Description, guild.ApproximateMemberCount, guild.MaxMembers, guild.PremiumTier, len(guild.Emojis), guild.OwnerID,
	)

	if guild.VanityURLCode != "" {
		desc += "\n\nhttps://discord.gg/" + guild.VanityURLCode
	} else {
		inv, err := s.GuildInvites(guild.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get guild invites")
			services.CaptureError(err)
			desc += "\n\nFailed to get guild invites"
		} else {
			if len(inv) > 0 {
				desc += "\n\nhttps://discord.gg/" + inv[0].Code
			}
		}
	}

	embed := components.NewEmbed().
		SetColor("DarkButNotBlack").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetDescription(desc).
		SetFooter(guild.ID).
		SetTimestamp().
		MessageEmbed

	s.ChannelMessageSendEmbed(m.ChannelID, embed)
}
