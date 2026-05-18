package owner

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	Commands["cmdstats"] = &Command{
		Name:    "cmdstats",
		Handler: cmdstats,
	}
}

func cmdstats(s *discordgo.Session, m *discordgo.MessageCreate) {
	// the command to get stats for is <prefix>cmdstats <command>
	command := strings.Split(m.Content, " ")[1]

	if command == "" {
		s.ChannelMessageSend(m.ChannelID, "Please provide a command")
		return
	}

	count := services.Redis.HGet(services.Redis.Context(), "seeds:cmds", command).Val()

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command `%s` has been run `%s` times", command, count))
}
