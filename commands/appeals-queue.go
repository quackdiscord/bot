package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var appealsQueueCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "queue",
	Description: "Get the queue of pending appeals",
}

func handleAppealsQueue(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)

	appeals, err := storage.GetOpenAppeals(i.GuildID)
	if err != nil {
		log.Error().Msgf("Failed to get open appeals: %s", err.Error())
		return EmbedResponse(components.ErrorEmbed("Failed to get open appeals."), true)
	}

	if len(appeals) == 0 {
		embed := components.NewEmbed().
			SetDescription("There are no pending appeals.").
			SetColor("Main").
			MessageEmbed
		return EmbedResponse(embed, false)
	}

	description := generateAppealsDescription(appeals)
	embed := components.NewEmbed().
		SetDescription(description).
		SetColor("Main").
		SetAuthor("Appeals Queue", guild.IconURL("")).
		MessageEmbed
	return EmbedResponse(embed, false)

}

func generateAppealsDescription(appeals []*structs.Appeal) string {
	const maxAppeals = 7

	// get the appeals setting for the channel id
	as, err := storage.FindAppealSettingsByGuildID(appeals[0].GuildID)
	if err != nil {
		log.Error().Msgf("Failed to find appeals settings: %s", err.Error())
		return "Something went wrong."
	}

	channelID := ""
	if as != nil {
		channelID = as.ChannelID
	}

	header := fmt.Sprintf("**%d** pending appeals\n\n", len(appeals))
	var builder strings.Builder
	builder.WriteString(header)

	// Show at most 7 appeals
	appealsToShow := appeals
	if len(appeals) > maxAppeals {
		appealsToShow = appeals[:maxAppeals]
	}

	for _, a := range appealsToShow {
		var banCase *structs.Case
		if a.CaseID.Valid {
			bc, err := storage.FindCaseByID(a.CaseID.String, a.GuildID)
			if err != nil {
				log.Error().Msgf("Failed to find ban case: %s", err.Error())
				banCase = nil
			} else {
				banCase = bc
			}
		}

		parsedTime, _ := time.Parse("2006-01-02 15:04:05", a.CreatedAt)
		unixTime := parsedTime.Unix()

		if banCase != nil {
			banTime, _ := time.Parse("2006-01-02 15:04:05", banCase.CreatedAt)
			banUnix := banTime.Unix()
			builder.WriteString(fmt.Sprintf("Created <t:%d:R> - banned <t:%d:R>\n", unixTime, banUnix))
		} else {
			builder.WriteString(fmt.Sprintf("Created <t:%d:R>\n", unixTime))
		}

		if channelID != "" && a.ReviewMessageID.Valid && a.ReviewMessageID.String != "" {
			builder.WriteString(fmt.Sprintf("<:text2:1229344477131309136> [Jump to Review](https://discord.com/channels/%s/%s/%s)\n", a.GuildID, channelID, a.ReviewMessageID.String))
		}
		builder.WriteString(fmt.Sprintf("<:text:1229343822337802271> `ID: %s`\n\n", a.ID))
	}

	// Add "more" message if there are additional appeals
	if len(appeals) > maxAppeals {
		builder.WriteString(fmt.Sprintf("(+%d more appeals)", len(appeals)-maxAppeals))
	}

	return builder.String()
}
