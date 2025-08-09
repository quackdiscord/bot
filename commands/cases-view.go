package commands

import (
	"fmt"
	"math"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

var casesViewCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "view",
	Description: "Ways to view various cases",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "latest",
			Description: "View the latest case in the server",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "user",
			Description: "View all cases for a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to view cases for",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "id",
			Description: "View a specific case by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the case to view",
					Required:    true,
				},
			},
		},
	},
}

func handleCasesViewLatest(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	c, err := storage.FindLatestCase(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch latest case", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch latest case."), true)
	}

	embed := generateCaseEmbed(s, c)

	return EmbedResponse(embed, false)
}

func handleCasesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	c, err := storage.FindCaseByID(caseID, i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch case by id", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch case."), true)
	}

	embed := generateCaseEmbed(s, c)

	return EmbedResponse(embed, false)
}

func handleCasesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	// pagination setup
	const pageSize = 5
	page := 1

	total, err := storage.CountCasesByUserID(user.ID, i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to count user cases", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch user's cases."), true)
	}

	if total == 0 {
		embed := components.NewEmbed().SetDescription("<@" + user.ID + "> has no cases.").SetColor("Main").MessageEmbed
		return EmbedResponse(embed, false)
	}

	cases, err := storage.FindCasesByUserIDPaginated(user.ID, i.GuildID, pageSize, 0)
	if err != nil {
		log.Error().AnErr("Failed to fetch user cases", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch user's cases."), true)
	}

	content := fmt.Sprintf("<@%s> has **%d** cases\n\n", user.ID, total)
	for _, c := range cases {
		moderator, _ := s.User(c.ModeratorID)
		if moderator == nil {
			moderator = &discordgo.User{Username: "Unknown"}
		}
		content += generateCaseDetails(c, moderator)
	}

	totalPages := int(math.Ceil(float64(total) / float64(pageSize)))

	embed := components.NewEmbed().
		SetDescription(content).
		SetTimestamp().
		SetAuthor("Cases for "+user.Username, user.AvatarURL("")).
		SetFooter(fmt.Sprintf("u:%s|g:%s|p:%d|ps:%d|c:%d", user.ID, i.GuildID, page, pageSize, total)).
		SetColor("Main").MessageEmbed

	prevDisabled := page <= 1
	nextDisabled := page >= totalPages

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{CustomID: "cases-view-prev", Label: "Prev", Style: discordgo.SecondaryButton, Disabled: prevDisabled},
						discordgo.Button{CustomID: "cases-view-next", Label: "Next", Style: discordgo.PrimaryButton, Disabled: nextDisabled},
					},
				},
			},
			AllowedMentions: new(discordgo.MessageAllowedMentions),
		},
	}
}

// generate a case embed from a case
func generateCaseEmbed(s *discordgo.Session, c *structs.Case) *discordgo.MessageEmbed {
	if c == nil {
		return components.ErrorEmbed("Case not found.")
	}

	user, _ := s.User(c.UserID)
	moderator, _ := s.User(c.ModeratorID)

	if user == nil || moderator == nil {
		return components.ErrorEmbed("Failed to fetch user or moderator.")
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> (%s)'s Case \n\n", user.ID, user.Username)+generateCaseDetails(c, moderator)).
		SetAuthor(fmt.Sprintf("Case %s", c.ID), user.AvatarURL("")).
		SetTimestamp().
		SetColor("Main").MessageEmbed

	return embed
}

func generateCaseDetails(c *structs.Case, moderator *discordgo.User) string {
	parsedTime, _ := time.Parse("2006-01-02 15:04:05", c.CreatedAt)
	unixTime := parsedTime.Unix()

	typeStr := "Case added"
	switch c.Type {
	case 0:
		typeStr = "Warned"
	case 1:
		typeStr = "Banned"
	case 2:
		typeStr = "Kicked"
	case 3:
		typeStr = "Unbanned"
	case 4:
		typeStr = "Timed out"
	}

	details := fmt.Sprintf(
		"-# <:text6:1321325229213089802> *ID: %s*\n<:text4:1229350683057324043> **%s** <t:%d:R> by <@%s>\n<:text:1229343822337802271> `%s`\n\n",
		c.ID, typeStr, unixTime, moderator.ID, c.Reason,
	)
	return details
}
