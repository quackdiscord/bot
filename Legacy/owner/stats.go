package owner

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	Commands["stats"] = &Command{
		Name:    "stats",
		Handler: stats,
	}
}

func stats(s *discordgo.Session, m *discordgo.MessageCreate) {
	stats := utils.GetBotStats(s)
	msg := utils.FormatStatsAsCodeBlock(stats)
	s.ChannelMessageSend(m.ChannelID, msg)
}
