package owner

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

func init() {
	Commands["modcmdstats"] = &Command{
		Name:    "modcmdstats",
		Handler: modcmdstats,
	}
}

func modcmdstats(s *discordgo.Session, m *discordgo.MessageCreate) {
	cmd := strings.Split(m.Content, " ")[1]
	user := strings.Split(m.Content, " ")[2]
	count := 0
	typeint := 0

	switch cmd {
	case "ban":
		typeint = 1
	case "warn":
		typeint = 0
	case "kick":
		typeint = 2
	case "timeout":
		typeint = 4
	default:
		s.ChannelMessageSend(m.ChannelID, "Invalid command")
		return
	}

	user = strings.TrimPrefix(user, "<@")
	user = strings.TrimSuffix(user, ">")

	row := services.DB.QueryRow("SELECT COUNT(*) FROM cases WHERE moderator_id = ? AND type = ? AND guild_id = ?", user, typeint, m.GuildID)
	err := row.Scan(&count)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get mod cmd stats")
		services.CaptureError(err)
		s.ChannelMessageSend(m.ChannelID, "Failed to get mod cmd stats")
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command `%s` has been run `%d` times by <@%s> in this server.", cmd, count, user))
}
