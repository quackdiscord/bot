package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

var staffTargetsCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "targets",
	Description: "View users this staff member actions most frequently",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "mod",
			Description: "The staff member to view targets for",
			Required:    true,
		},
	},
}

func handleStaffTargets(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	options := i.ApplicationCommandData().Options[0].Options
	mod := options[0].UserValue(s)

	go func() {
		targets, err := storage.GetModFrequentTargets(mod.ID, i.GuildID, 15)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get mod targets")
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch targets.")},
			})
			return
		}

		if len(targets) == 0 {
			embed := components.NewEmbed().
				SetDescription(fmt.Sprintf("<@%s> has not actioned any users.", mod.ID)).
				SetColor("Main").
				MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// Calculate total for percentages
		var total int
		for _, t := range targets {
			total += t.CaseCount
		}

		// Build the list
		var listStr string
		for idx, t := range targets {
			pct := float64(t.CaseCount) / float64(total) * 100
			listStr += fmt.Sprintf(
				"**%d.** <@%s> - **%d** cases (%.1f%%)\n",
				idx+1,
				t.UserID,
				t.CaseCount,
				pct,
			)
		}

		embed := components.NewEmbed().
			SetAuthor(fmt.Sprintf("%s's Most Actioned Users", mod.Username), mod.AvatarURL("")).
			SetDescription(listStr).
			SetColor("Main").
			SetFooter(fmt.Sprintf("Total: %d cases across %d users", total, len(targets))).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}
