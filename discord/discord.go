package discord

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

type events = map[string]*structs.DiscordEvent
type commands = map[string]*structs.DiscordCommand

var Session *discordgo.Session
var Commands = make(commands, 0)
var Events = make(events, 0)

var s *storage.Store

// Connect inits the session and opens the connection
func Connect(st *storage.Store) {
	s = st
	log.Info().Msg("Connecting to Discord")

	token := lib.Config.Discord.Token

	// create the session
	var err error
	Session, err = discordgo.New(token)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create discord session")
	}

	// apply intents and state
	Session.Identify.Intents = discordgo.Intent(3276543)
	Session.StateEnabled = true
	Session.State.MaxMessageCount = 5000

	// open the session
	err = Session.Open()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to open discord session")
	}

	registerEvents()
	// register commands here later
}

func registerEvents() {
	for name, event := range Events {
		Session.AddHandler(event.Handler)
		log.Info().Msgf("Registered event: %s", name)
	}
	log.Info().Msgf("Registered %d events", len(Events))
}
