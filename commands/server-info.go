package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/log"
)

var serverInfoCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "info",
	Description: "Get some info about the server",
}

func handleServerInfo(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, err := s.GuildWithCounts(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch a guild", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch guild info."), true)
	}

	desc := fmt.Sprintf(
		"**Description:**\n<:text:1229343822337802271>%s\n\n**Numbers**\n<:text2:1229344477131309136>**Members:** %d / %d\n<:text2:1229344477131309136>**Boost Tier:** %d\n<:text:1229343822337802271>**Emojis:** %d\n\n**Owner:**\n<:text:1229343822337802271> <:owner:1230302954683367436> <@%s>",
		guild.Description, guild.ApproximateMemberCount, guild.MaxMembers, guild.PremiumTier, len(guild.Emojis), guild.OwnerID,
	)

	embed := components.NewEmbed().
		SetColor("DarkButNotBlack").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetDescription(desc).
		SetFooter(guild.ID).
		SetTimestamp().
		MessageEmbed

	return EmbedResponse(embed, false)
}
