package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
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
	go func() {
		c, err := storage.FindLatestCase(i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch latest case")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch latest case.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := generateCaseEmbed(s, c)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleCasesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	go func() {
		c, err := storage.FindCaseByID(caseID, i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch case by id")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch case.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := generateCaseEmbed(s, c)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleCasesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		cases, err := storage.FindCasesByUserID(user.ID, i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch user cases")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch user's cases.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		if len(cases) == 0 {
			embed := components.NewEmbed().SetDescription("<@" + user.ID + "> has no cases.").SetColor("Main").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		content := fmt.Sprintf("<@%s> has **%d** cases\n\n", user.ID, len(cases))

		for _, c := range cases {
			moderator, _ := s.User(c.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}

			content += *generateCaseDetails(c, moderator)
		}

		// if the content is > 2048 characters, cut it off and add "too many to show..."
		if len(content) > 2048 {
			content = content[:2000] + "\n\n*Too many cases to show. You should ban them...*"
		}

		embed := components.NewEmbed().
			SetDescription(content).
			SetTimestamp().
			SetAuthor("Cases for "+user.Username, user.AvatarURL("")).
			SetColor("Main").MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			// Content: &content,
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

// generate a case embed from a case
func generateCaseEmbed(s *discordgo.Session, c *structs.Case) *discordgo.MessageEmbed {
	if c == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Case not found.").SetColor("Error").MessageEmbed
		return embed
	}

	user, _ := s.User(c.UserID)
	moderator, _ := s.User(c.ModeratorID)

	if user == nil || moderator == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch user or moderator.").SetColor("Error").MessageEmbed
		return embed
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> (%s)'s Case \n\n", user.ID, user.Username)+*generateCaseDetails(c, moderator)).
		SetAuthor(fmt.Sprintf("Case %s", c.ID), user.AvatarURL("")).
		SetTimestamp().
		SetColor("Main").MessageEmbed

	return embed
}

func generateCaseDetails(c *structs.Case, moderator *discordgo.User) *string {
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
		"<t:%d:R> %s by %s\n<:text2:1229344477131309136> *\"%s\"*\n<:text:1229343822337802271> `ID: %s`\n\n",
		unixTime, typeStr, moderator.Username, c.Reason, c.ID,
	)
	return &details
}
