package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

var staffListCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "list",
	Description: "List all staff members with summary stats",
}

func handleStaffList(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	go func() {
		mods, err := storage.GetModsSortedByCases(i.GuildID, 15)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mods list")
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch staff list.")},
			})
			return
		}

		if len(mods) == 0 {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("No active staff in the last 30 days.")},
			})
			return
		}

		// Build the list
		var listStr string
		for idx, m := range mods {
			// Format last action time
			lastAction := "Never"
			if m.LastActionAt.Valid {
				if t, err := time.Parse("2006-01-02 15:04:05", m.LastActionAt.String); err == nil {
					lastAction = fmt.Sprintf("<t:%d:R>", t.Unix())
				}
			}

			// Build row
			listStr += fmt.Sprintf(
				"**%d.** <@%s>\n> Cases: **%d** | Tickets: **%d** | Appeals: **%d** | Last: %s\n\n",
				idx+1,
				m.ModID,
				m.TotalCases,
				m.TicketsResolved,
				m.AppealsHandled,
				lastAction,
			)
		}

		// Calculate totals
		var totalCases, totalTickets, totalAppeals int
		for _, m := range mods {
			totalCases += m.TotalCases
			totalTickets += m.TicketsResolved
			totalAppeals += m.AppealsHandled
		}

		embed := components.NewEmbed().
			SetAuthor("Staff Activity Leaderboard", "").
			SetDescription(listStr).
			SetColor("Main").
			AddField("Active Staff", fmt.Sprintf("**%d**", len(mods))).
			AddField("Total Cases", fmt.Sprintf("**%d**", totalCases)).
			AddField("Total Tickets", fmt.Sprintf("**%d**", totalTickets)).
			AddField("Total Appeals", fmt.Sprintf("**%d**", totalAppeals)).
			InlineAllFields().
			SetFooter("Active in last 30 days, sorted by case count").
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}
