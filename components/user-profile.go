package components

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
)

func init() {
	Components["user-view-cases"] = handleUserViewCases
	Components["user-view-notes"] = handleUserViewNotes
	Components["user-copy-id"] = handleUserCopyID
}

func handleUserViewCases(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// extract user ID from custom ID (format: "user-view-cases:userID")
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) < 2 {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Invalid button data.",
		})
	}
	userID := parts[1]

	go func() {
		user, err := s.User(userID)
		if err != nil {
			log.Error().AnErr("Failed to fetch user", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{ErrorEmbed("Failed to fetch user.")}})
			return
		}

		const pageSize = 5
		page := 1

		total, err := storage.CountCasesByUserID(userID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to count user cases", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{ErrorEmbed("Failed to fetch user's cases.")}})
			return
		}

		if total == 0 {
			embed := NewEmbed().SetDescription("<@" + userID + "> has no cases.").SetColor("Main").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
			return
		}

		cases, err := storage.FindCasesByUserIDPaginated(userID, i.GuildID, pageSize, 0)
		if err != nil {
			log.Error().AnErr("Failed to fetch user cases", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{ErrorEmbed("Failed to fetch user's cases.")}})
			return
		}

		content := ""
		for _, c := range cases {
			moderator, _ := s.User(c.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}
			content += utils.GenerateCaseDetails(c, moderator)
		}

		totalPages := int(math.Ceil(float64(total) / float64(pageSize)))

		embed := NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has **%d** cases\n\n", userID, total)+content).
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

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	}
}

func handleUserViewNotes(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// extract user ID from custom ID (format: "user-view-notes:userID")
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) < 2 {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Invalid button data.",
		})
	}
	userID := parts[1]

	go func() {
		user, err := s.User(userID)
		if err != nil {
			log.Error().AnErr("Failed to fetch user", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{ErrorEmbed("Failed to fetch user.")}})
			return
		}

		notes, err := storage.FindNoteByUserID(userID, i.GuildID)
		if err != nil {
			log.Error().AnErr("Failed to fetch notes by user id", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{ErrorEmbed("Failed to fetch notes.")}})
			return
		}

		if len(notes) == 0 {
			embed := NewEmbed().SetDescription("<@" + userID + "> has no notes.").SetColor("Main").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
			return
		}

		content := fmt.Sprintf("<@%s> has **%d** notes\n\n", userID, len(notes))

		for _, n := range notes {
			moderator, _ := s.User(n.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}
			content += generateNoteDetails(n, moderator)
		}

		// if the content is > 2048 characters, cut it off
		if len(content) > 2048 {
			content = content[:2000] + "\n\n*Too many notes to show them all.*"
		}

		embed := NewEmbed().
			SetDescription(content).
			SetTimestamp().
			SetAuthor("Notes for "+user.Username, user.AvatarURL("")).
			SetColor("Main").MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{Embeds: &[]*discordgo.MessageEmbed{embed}})
	}()

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	}
}

func handleUserCopyID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// extract user ID from custom ID (format: "user-copy-id:userID")
	parts := strings.Split(i.MessageComponentData().CustomID, ":")
	if len(parts) < 2 {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Invalid button data.",
		})
	}
	userID := parts[1]

	return ComplexResponse(&discordgo.InteractionResponseData{
		Flags:   discordgo.MessageFlagsEphemeral,
		Content: fmt.Sprintf("```%s```", userID),
	})
}

func generateNoteDetails(n *structs.Note, moderator *discordgo.User) string {
	parsedTime, _ := time.Parse("2006-01-02 15:04:05", n.CreatedAt)
	unixTime := parsedTime.Unix()

	return fmt.Sprintf(
		"<t:%d:R> by %s\n<:text2:1229344477131309136> *\"%s\"*\n<:text:1229343822337802271> `ID: %s`\n\n",
		unixTime, moderator.Username, n.Content, n.ID,
	)
}
