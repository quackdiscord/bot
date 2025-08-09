package components

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

// footer format: u:<userID>|g:<guildID>|p:<page>|ps:<pageSize>|c:<count>
var footerRe = regexp.MustCompile(`u:([^|]+)\|g:([^|]+)\|p:(\d+)\|ps:(\d+)\|c:(\d+)`)

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
	if i.Message == nil || len(i.Message.Embeds) == 0 || i.Message.Embeds[0].Footer == nil {
		return EmptyResponse()
	}

	footer := i.Message.Embeds[0].Footer.Text
	m := footerRe.FindStringSubmatch(footer)
	if m == nil {
		return EmptyResponse()
	}

	userID := m[1]
	guildID := m[2]
	page, _ := strconv.Atoi(m[3])
	pageSize, _ := strconv.Atoi(m[4])
	count, _ := strconv.Atoi(m[5])

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
		return UpdateResponse(&discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{i.Message.Embeds[0]}})
	}

	// rebuild content
	content := fmt.Sprintf("<@%s> has **%d** cases\n\n", userID, count)
	for _, c := range cases {
		moderator, _ := s.User(c.ModeratorID)
		if moderator == nil {
			moderator = &discordgo.User{Username: "Unknown"}
		}
		content += generateCaseDetailsForComponents(c, moderator)
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
	embed := NewEmbed().
		SetDescription(content).
		SetTimestamp().
		SetAuthor(authorName, authorIcon).
		SetFooter(fmt.Sprintf("u:%s|g:%s|p:%d|ps:%d|c:%d", userID, guildID, page, pageSize, count)).
		SetColor("Main").MessageEmbed

	prevDisabled := page <= 1
	nextDisabled := page >= totalPages

	return UpdateResponse(&discordgo.InteractionResponseData{
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
	})
}

// generateCaseDetailsForComponents mirrors commands.generateCaseDetails without importing commands.
func generateCaseDetailsForComponents(c *structs.Case, moderator *discordgo.User) string {
	// duplicate minimal formatting to avoid import cycle
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

	return fmt.Sprintf(
		"-# <:text6:1321325229213089802> *ID: %s*\n<:text4:1229350683057324043> **%s** <t:%d:R> by <@%s>\n<:text:1229343822337802271> `%s`\n\n",
		c.ID, typeStr, unixTime, moderator.ID, c.Reason,
	)
}
