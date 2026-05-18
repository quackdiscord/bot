package utils

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/structs"
)

func GenerateCaseDetails(c *structs.Case, moderator *discordgo.User) string {
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
		"-# <:text6:1321325229213089802> *ID: %s*\n<:text4:1229350683057324043> **%s** <t:%d:R> by <@%s>",
		c.ID, typeStr, unixTime, moderator.ID,
	)

	if c.ContextURL.Valid {
		details += fmt.Sprintf("\n<:text4:1229350683057324043> `%s`\n<:text:1229343822337802271> [View Context](%s)\n\n", c.Reason, c.ContextURL.String)
	} else {
		details += fmt.Sprintf("\n<:text:1229343822337802271> `%s`\n\n", c.Reason)
	}
	return details
}
