package components

import (
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/utils"
)

// description header format: <@userID> has **count** cases\n\n
var descRe = regexp.MustCompile(`^<@([0-9]+)> has \*\*([0-9]+)\*\* cases`)
var footerRe = regexp.MustCompile(`^Page ([0-9]+) of ([0-9]+)$`)

func init() {
	Components["cases-view-prev"] = handleCasesPrev
	Components["cases-view-next"] = handleCasesNext
}

func handleCasesPrev(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	return handleCasesPaginate(s, i, -1)
}

func handleCasesNext(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	return handleCasesPaginate(s, i, 1)
}

func handleCasesPaginate(s *discordgo.Session, i *discordgo.InteractionCreate, delta int) *discordgo.InteractionResponse {
	// immediately defer the update to avoid timeouts and do work in background
	go func() {
		if i.Message == nil || len(i.Message.Embeds) == 0 {
			return
		}

		desc := i.Message.Embeds[0].Description
		m := descRe.FindStringSubmatch(desc)
		if m == nil {
			return
		}

		userID := m[1]
		count, _ := strconv.Atoi(m[2])
		// totalPages from description not present; recompute from count and parse page from footer
		pageSize := 5
		guildID := i.GuildID

		// parse footer
		if i.Message.Embeds[0].Footer == nil {
			return
		}
		f := i.Message.Embeds[0].Footer.Text
		fm := footerRe.FindStringSubmatch(f)
		if fm == nil {
			return
		}
		page, _ := strconv.Atoi(fm[1])

		// bounds
		totalPages := int(math.Ceil(float64(count) / float64(pageSize)))
		page += delta
		if page < 1 {
			page = 1
		}
		if page > totalPages {
			page = totalPages
		}

		// fetch cases
		offset := (page - 1) * pageSize
		cases, err := storage.FindCasesByUserIDPaginated(userID, guildID, pageSize, offset)
		if err != nil {
			log.Error().AnErr("Failed to fetch paginated cases", err)
			return
		}

		// rebuild content
		content := ""
		for _, c := range cases {
			moderator, _ := s.User(c.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}
			content += utils.GenerateCaseDetails(c, moderator)
		}

		// author
		authorName := "Cases"
		authorIcon := ""
		if i.Message.Embeds[0].Author != nil {
			authorName = i.Message.Embeds[0].Author.Name
			authorIcon = i.Message.Embeds[0].Author.IconURL
		} else if u, _ := s.User(userID); u != nil {
			authorName = "Cases for " + u.Username
			authorIcon = u.AvatarURL("")
		}

		// update embed
		totalPages = int(math.Ceil(float64(count) / float64(pageSize)))
		embed := NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has **%d** cases\n\n", userID, count)+content).
			SetTimestamp().
			SetAuthor(authorName, authorIcon).
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

	return EmptyResponse()
}
