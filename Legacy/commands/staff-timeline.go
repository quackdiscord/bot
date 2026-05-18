package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var staffTimelineCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "timeline",
	Description: "View a staff member's recent actions timeline",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "mod",
			Description: "The staff member to view timeline for",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionInteger,
			Name:        "days",
			Description: "Number of days to look back (default: 7, max: 30)",
			Required:    false,
			MinValue:    &[]float64{1}[0],
			MaxValue:    30,
		},
	},
}

func handleStaffTimeline(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	options := i.ApplicationCommandData().Options[0].Options
	mod := options[0].UserValue(s)

	// Default to 7 days
	days := 7
	if len(options) > 1 {
		days = int(options[1].IntValue())
	}

	go func() {
		// Get recent cases for this mod
		cases, err := storage.GetModRecentCasesInDays(mod.ID, i.GuildID, days, 15)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod timeline")
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch timeline.")},
			})
			return
		}

		if len(cases) == 0 {
			embed := components.NewEmbed().
				SetDescription(fmt.Sprintf("<@%s> has no actions in the last %d days.", mod.ID, days)).
				SetColor("Main").
				MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// Build timeline
		embed := buildTimelineEmbed(mod, cases, days)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}

func buildTimelineEmbed(mod *discordgo.User, cases []*structs.Case, days int) *discordgo.MessageEmbed {
	var timeline string

	for _, c := range cases {
		// Parse timestamp
		parsedTime, _ := time.Parse("2006-01-02 15:04:05", c.CreatedAt)
		unixTime := parsedTime.Unix()

		// Truncate reason if too long
		reason := c.Reason
		if len(reason) > 40 {
			reason = reason[:37] + "..."
		}

		timeline += fmt.Sprintf(
			"<t:%d:R> %s <@%s>\n> `%s` - *%s*\n\n",
			unixTime,
			getTimelineTypeString(c.Type),
			c.UserID,
			c.ID,
			reason,
		)
	}

	// Truncate if too long
	if len(timeline) > 2048 {
		timeline = timeline[:2000] + "\n\n*Timeline truncated...*"
	}

	embed := components.NewEmbed().
		SetAuthor(fmt.Sprintf("%s's Timeline (Last %d days)", mod.Username, days), mod.AvatarURL("")).
		SetDescription(timeline).
		SetColor("Main").
		SetFooter(fmt.Sprintf("Showing %d most recent actions", len(cases))).
		SetTimestamp().
		MessageEmbed

	return embed
}

func getTimelineTypeString(caseType int8) string {
	switch caseType {
	case 0:
		return "warned"
	case 1:
		return "banned"
	case 2:
		return "kicked"
	case 3:
		return "unbanned"
	case 4:
		return "timed out"
	case 5:
		return "deleted message from"
	default:
		return "actioned"
	}
}
