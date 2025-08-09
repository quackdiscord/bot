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
	// respond immediately and do work in background
	go func() {
		c, err := storage.FindLatestCase(i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to fetch latest case", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch latest case.")}})
			return
		}

		embed := generateCaseEmbed(s, c)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
	}()

	return LoadingResponse()
}

func handleCasesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	go func() {
		c, err := storage.FindCaseByID(caseID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to fetch case by id", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch case.")}})
			return
		}

		embed := generateCaseEmbed(s, c)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
	}()

	return LoadingResponse()
}

func handleCasesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		// pagination setup
		const pageSize = 5
		page := 1

		total, err := storage.CountCasesByUserID(user.ID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to count user cases", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch user's cases.")}})
			return
		}

		if total == 0 {
			embed := components.NewEmbed().SetDescription("<@" + user.ID + "> has no cases.").SetColor("Main").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
			return
		}

		cases, err := storage.FindCasesByUserIDPaginated(user.ID, i.GuildID, pageSize, 0)
		if err != nil {
			log.Error().AnErr("Failed to fetch user cases", err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch user's cases.")}})
			return
		}

		content := ""
		for _, c := range cases {
			moderator, _ := s.User(c.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}
			content += generateCaseDetails(c, moderator)
		}

		totalPages := int(math.Ceil(float64(total) / float64(pageSize)))

		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has **%d** cases\n\n", user.ID, total)+content).
			SetTimestamp().
			SetAuthor("Cases for "+user.Username, user.AvatarURL("")).
			SetFooter(fmt.Sprintf("Page %d of %d", page, totalPages)).
			SetColor("Main").MessageEmbed

		prevDisabled := page <= 1
		nextDisabled := page >= totalPages

		comps := []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{CustomID: "cases-view-prev", Label: "Prev", Style: discordgo.SecondaryButton, Disabled: prevDisabled},
					discordgo.Button{CustomID: "cases-view-next", Label: "Next", Style: discordgo.PrimaryButton, Disabled: nextDisabled},
				},
			},
		}

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}, Components: &comps})
	}()

	return LoadingResponse()
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
