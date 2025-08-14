package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
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
	const maxDescriptionLen = 4096

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
	currentLen := len(header)

	shown := 0
	for _, a := range appeals {
		var block strings.Builder

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
			block.WriteString(fmt.Sprintf("Created <t:%d:R> - banned <t:%d:R>\n", unixTime, banUnix))
		} else {
			block.WriteString(fmt.Sprintf("Created <t:%d:R>\n", unixTime))
		}

		if channelID != "" && a.ReviewMessageID.Valid && a.ReviewMessageID.String != "" {
			block.WriteString(fmt.Sprintf("<:text2:1229344477131309136> [Jump to Review](https://discord.com/channels/%s/%s/%s)\n", a.GuildID, channelID, a.ReviewMessageID.String))
		}
		block.WriteString(fmt.Sprintf("<:text:1229343822337802271> `ID: %s`\n\n", a.ID))

		candidate := block.String()
		if currentLen+len(candidate) > maxDescriptionLen {
			break
		}

		builder.WriteString(candidate)
		currentLen += len(candidate)
		shown++
	}

	if shown < len(appeals) {
		footer := fmt.Sprintf("(+%d more appeals)", len(appeals)-shown)
		// ensure footer fits; if not, try a shorter variant
		if currentLen+len(footer)+1 > maxDescriptionLen { // +1 for potential newline
			footer = fmt.Sprintf("(+%d more)", len(appeals)-shown)
		}
		if currentLen+len(footer)+1 <= maxDescriptionLen {
			builder.WriteString(footer)
		}
	}

	return builder.String()
}
