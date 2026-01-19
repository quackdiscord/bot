package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var staffViewCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "view",
	Description: "View a staff member's profile and stats",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "mod",
			Description: "The staff member to view",
			Required:    true,
		},
	},
}

func handleStaffView(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// Get the user option from the subcommand
	mod := i.ApplicationCommandData().Options[0].Options[0].UserValue(s)

	go func() {
		// Fetch all stats in parallel
		caseStats, err := storage.GetModCaseStats(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod case stats")
			services.CaptureError(err)
			caseStats = &structs.ModCaseStats{}
		}

		ticketStats, err := storage.GetModTicketStats(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod ticket stats")
			services.CaptureError(err)
			ticketStats = &structs.ModTicketStats{}
		}

		appealStats, err := storage.GetModAppealStats(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod appeal stats")
			services.CaptureError(err)
			appealStats = &structs.ModAppealStats{}
		}

		noteStats, err := storage.GetModNoteStats(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod note stats")
			services.CaptureError(err)
			noteStats = &structs.ModNoteStats{}
		}

		recentCases, err := storage.GetModRecentCases(mod.ID, i.GuildID, 3)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod recent cases")
			services.CaptureError(err)
			recentCases = []*structs.Case{}
		}

		frequentTargets, err := storage.GetModFrequentTargets(mod.ID, i.GuildID, 3)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod frequent targets")
			services.CaptureError(err)
			frequentTargets = []storage.ModFrequentTarget{}
		}

		adminNotesCount, err := storage.CountModNotesByModID(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get admin notes count")
			services.CaptureError(err)
			adminNotesCount = 0
		}

		// Build the embed
		embed := buildStaffViewEmbed(mod, caseStats, ticketStats, appealStats, noteStats, recentCases, frequentTargets, adminNotesCount)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}

func buildStaffViewEmbed(
	mod *discordgo.User,
	caseStats *structs.ModCaseStats,
	ticketStats *structs.ModTicketStats,
	appealStats *structs.ModAppealStats,
	noteStats *structs.ModNoteStats,
	recentCases []*structs.Case,
	frequentTargets []storage.ModFrequentTarget,
	adminNotesCount int,
) *discordgo.MessageEmbed {

	// Build case breakdown string
	caseBreakdown := fmt.Sprintf(
		"<:text2:1229344477131309136> Warns: **%d**\n<:text2:1229344477131309136> Bans: **%d**\n<:text2:1229344477131309136> Kicks: **%d**\n<:text2:1229344477131309136> Timeouts: **%d**\n<:text:1229343822337802271> Unbans: **%d**",
		caseStats.Warns, caseStats.Bans, caseStats.Kicks, caseStats.Timeouts, caseStats.Unbans,
	)

	// Build activity string (last 24h / 7d / 30d)
	activityStr := fmt.Sprintf(
		"<:text2:1229344477131309136> Last 24h: **%d** cases\n<:text2:1229344477131309136> Last 7d: **%d** cases\n<:text:1229343822337802271> Last 30d: **%d** cases",
		caseStats.CasesLast24h, caseStats.CasesLast7d, caseStats.CasesLast30d,
	)

	// Build recent cases string
	recentCasesStr := "None"
	if len(recentCases) > 0 {
		recentCasesStr = ""
		for idx, c := range recentCases {
			prefix := "<:text2:1229344477131309136>"
			if idx == len(recentCases)-1 {
				prefix = "<:text:1229343822337802271>"
			}
			recentCasesStr += fmt.Sprintf("%s `%s` %s <@%s>\n", prefix, c.ID, getCaseTypeString(c.Type), c.UserID)
		}
	}

	// Build frequent targets string
	frequentTargetsStr := "None"
	if len(frequentTargets) > 0 {
		frequentTargetsStr = ""
		for idx, t := range frequentTargets {
			prefix := "<:text2:1229344477131309136>"
			if idx == len(frequentTargets)-1 {
				prefix = "<:text:1229343822337802271>"
			}
			frequentTargetsStr += fmt.Sprintf("%s <@%s> - **%d** cases\n", prefix, t.UserID, t.CaseCount)
		}
	}

	// Build top reasons string
	topReasonsStr := "None"
	if len(caseStats.TopReasons) > 0 {
		topReasonsStr = ""
		for idx, r := range caseStats.TopReasons {
			prefix := "<:text2:1229344477131309136>"
			if idx == len(caseStats.TopReasons)-1 {
				prefix = "<:text:1229343822337802271>"
			}
			reason := r.Reason
			if len(reason) > 30 {
				reason = reason[:27] + "..."
			}
			topReasonsStr += fmt.Sprintf("%s `%s` (**%d**)\n", prefix, reason, r.Count)
		}
	}

	embed := components.NewEmbed().
		SetAuthor(fmt.Sprintf("%s's Staff Profile", mod.Username), mod.AvatarURL("")).
		SetColor("Main").
		AddField("Total Cases", fmt.Sprintf("**%d**", caseStats.TotalCases)).
		AddField("Tickets Resolved", fmt.Sprintf("**%d**", ticketStats.TotalResolved)).
		AddField("Appeals Handled", fmt.Sprintf("**%d**", appealStats.TotalAppeals)).
		AddField("Notes Created", fmt.Sprintf("**%d**", noteStats.TotalNotes)).
		AddField("Admin Notes", fmt.Sprintf("**%d**", adminNotesCount)).
		AddField("Case Breakdown", caseBreakdown).
		AddField("Recent Activity", activityStr).
		AddField("Recent Cases", recentCasesStr).
		AddField("Frequent Targets", frequentTargetsStr).
		AddField("Top Reasons", topReasonsStr).
		SetFooter(fmt.Sprintf("Staff ID: %s", mod.ID)).
		SetTimestamp().
		InlineAllFields().
		MessageEmbed

	// Make certain fields full width (indices shifted due to Admin Notes field)
	if len(embed.Fields) >= 6 {
		embed.Fields[5].Inline = false // Case Breakdown
	}
	if len(embed.Fields) >= 7 {
		embed.Fields[6].Inline = false // Recent Activity
	}
	if len(embed.Fields) >= 8 {
		embed.Fields[7].Inline = false // Recent Cases
	}
	if len(embed.Fields) >= 9 {
		embed.Fields[8].Inline = false // Frequent Targets
	}
	if len(embed.Fields) >= 10 {
		embed.Fields[9].Inline = false // Top Reasons
	}

	return embed
}

func getCaseTypeString(caseType int8) string {
	switch caseType {
	case 0:
		return "Warned"
	case 1:
		return "Banned"
	case 2:
		return "Kicked"
	case 3:
		return "Unbanned"
	case 4:
		return "Timed out"
	case 5:
		return "Message Deleted"
	default:
		return "Unknown"
	}
}
