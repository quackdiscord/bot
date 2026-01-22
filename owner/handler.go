package owner

import (
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/log"
)

type Command struct {
	Name    string
	Handler func(s *discordgo.Session, m *discordgo.MessageCreate)
}

var Commands = make(map[string]*Command)
var Prefix = "!!!"

func Handle(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author != nil && m.Author.ID == config.Bot.BotOwnerID && strings.HasPrefix(m.Content, Prefix) {
		command := strings.TrimPrefix(strings.Split(m.Content, " ")[0], Prefix)

		cmd, ok := Commands[command]
		if !ok {
			log.Error().Msgf("Owner command not found: %s", command)
			return
		}

		cmd.Handler(s, m)
		log.Info().Msgf("Owner command: %s", command)
	}
}
