package commands

import (
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

var staffActivityCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "activity",
	Description: "View staff activity patterns",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "mod",
			Description: "View a staff member's activity heatmap",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "mod",
					Description: "The staff member to view activity for",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "server",
			Description: "View server-wide staff activity overview",
		},
	},
}

func handleStaffActivity(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	subCmd := i.ApplicationCommandData().Options[0].Options[0]

	switch subCmd.Name {
	case "mod":
		return handleStaffActivityMod(s, i)
	case "server":
		return handleStaffActivityServer(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}

func handleStaffActivityMod(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	mod := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		// Get activity by hour
		hourlyActivity, err := storage.GetModActivityByHour(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod hourly activity")
			services.CaptureError(err)
		}

		// Get activity by weekday
		weekdayActivity, err := storage.GetModActivityByWeekday(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod weekday activity")
			services.CaptureError(err)
		}

		// Get general activity stats
		activityStats, err := storage.GetModActivityStats(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod activity stats")
			services.CaptureError(err)
		}

		// Build the embed
		embed := buildActivityEmbed(mod.Username, mod.AvatarURL(""), hourlyActivity, weekdayActivity, activityStats.FirstActionAt, activityStats.LastActionAt, activityStats.DaysActive)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}

func handleStaffActivityServer(_ *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	go func() {
		s := services.Discord

		// Get server-wide activity by hour
		hourlyActivity, err := storage.GetServerActivityByHour(i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get server hourly activity")
			services.CaptureError(err)
		}

		// Get server-wide activity by weekday
		weekdayActivity, err := storage.GetServerActivityByWeekday(i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get server weekday activity")
			services.CaptureError(err)
		}

		guild, _ := s.Guild(i.GuildID)
		guildName := "Server"
		guildIcon := ""
		if guild != nil {
			guildName = guild.Name
			guildIcon = guild.IconURL("")
		}

		// Build the embed (no first/last action for server-wide)
		embed := buildActivityEmbed(guildName, guildIcon, hourlyActivity, weekdayActivity, time.Time{}, time.Time{}, 0)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}

func buildActivityEmbed(name, iconURL string, hourly [24]int, weekday [7]int, firstAction, lastAction time.Time, daysActive int) *discordgo.MessageEmbed {
	// Build hourly heatmap
	hourlyStr := buildHourlyHeatmap(hourly)

	// Build weekday heatmap
	weekdayStr := buildWeekdayHeatmap(weekday)

	// Find peak hour and day
	peakHour, peakHourCount := findPeak(hourly[:])
	peakDay, peakDayCount := findPeak(weekday[:])

	dayNames := []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

	// Calculate total
	var total int
	for _, c := range hourly {
		total += c
	}

	embed := components.NewEmbed().
		SetAuthor(fmt.Sprintf("%s's Activity", name), iconURL).
		SetColor("Main").
		AddField("Total Cases", fmt.Sprintf("**%d**", total)).
		AddField("Peak Hour", fmt.Sprintf("**%d:00** UTC (%d)", peakHour, peakHourCount)).
		AddField("Peak Day", fmt.Sprintf("**%s** (%d)", dayNames[peakDay], peakDayCount)).
		AddField("Hourly Activity (UTC)", hourlyStr).
		AddField("Weekly Activity", weekdayStr).
		SetTimestamp().
		InlineAllFields().
		MessageEmbed

	// Add first/last action if available
	if !firstAction.IsZero() {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "First Action",
			Value:  fmt.Sprintf("<t:%d:R>", firstAction.Unix()),
			Inline: true,
		})
	}
	if !lastAction.IsZero() {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Last Action",
			Value:  fmt.Sprintf("<t:%d:R>", lastAction.Unix()),
			Inline: true,
		})
	}
	if daysActive > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Days Active",
			Value:  fmt.Sprintf("**%d**", daysActive),
			Inline: true,
		})
	}

	// Make heatmap fields full width
	for idx, f := range embed.Fields {
		if f.Name == "Hourly Activity (UTC)" || f.Name == "Weekly Activity" {
			embed.Fields[idx].Inline = false
		}
	}

	return embed
}

func buildHourlyHeatmap(hours [24]int) string {
	// Group into time blocks for cleaner display
	// Morning (6-11), Afternoon (12-17), Evening (18-23), Night (0-5)
	blocks := []struct {
		name  string
		start int
		end   int
	}{
		{"Night (0-5)", 0, 5},
		{"Morning (6-11)", 6, 11},
		{"Afternoon (12-17)", 12, 17},
		{"Evening (18-23)", 18, 23},
	}

	var result string
	for _, block := range blocks {
		var sum int
		for h := block.start; h <= block.end; h++ {
			sum += hours[h]
		}
		result += fmt.Sprintf("> **%s:** %d cases\n", block.name, sum)
	}

	return result
}

func buildWeekdayHeatmap(days [7]int) string {
	dayLabels := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

	// Find max for scaling
	max := 1
	for _, c := range days {
		if c > max {
			max = c
		}
	}

	// Use code block for consistent monospace rendering
	result := "```\n"
	for i, label := range dayLabels {
		bar := buildBar(days[i], max, 12)
		result += fmt.Sprintf("%s %s %3d\n", label, bar, days[i])
	}
	result += "```"

	return result
}

func buildBar(value, max, width int) string {
	if max == 0 || value == 0 {
		return strings.Repeat("-", width)
	}
	filled := int(float64(value) / float64(max) * float64(width))
	if filled > width {
		filled = width
	}
	if filled == 0 && value > 0 {
		filled = 1
	}
	return strings.Repeat("#", filled) + strings.Repeat("-", width-filled)
}

func findPeak(values []int) (int, int) {
	peakIdx := 0
	peakVal := 0
	for i, v := range values {
		if v > peakVal {
			peakVal = v
			peakIdx = i
		}
	}
	return peakIdx, peakVal
}
