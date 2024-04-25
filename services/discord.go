package services

import (
	"os"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
)

var Discord *discordgo.Session
var Commands = make(map[string]*Command)
var RegisteredCommands = make([]*discordgo.ApplicationCommand, len(Commands))
var Enviorment = os.Getenv("ENVIORNMENT")

type Command struct {
	*discordgo.ApplicationCommand
	Handler func(*discordgo.Session, *discordgo.InteractionCreate) *discordgo.InteractionResponse
}

func ConnectDiscord(events []interface{}) {
	token := ""

	if Enviorment == "prod" {
		token = os.Getenv("TOKEN")
	} else {
		token = os.Getenv("DEV_TOKEN")
	}

	Discord, _ = discordgo.New(token)
	Discord.Identify.Intents |= discordgo.IntentGuildMembers
	Discord.Identify.Intents |= discordgo.IntentsAllWithoutPrivileged

	for _, h := range events {
		Discord.AddHandler(h)
	}

	err := Discord.Open()
	if err != nil {
		log.WithError(err).Fatal("Error opening connection to Discord")
	}

	// register commands
	// if Enviorment == "prod" {
	// 	RegisterCommands(Discord, "") // register globally
	// } else {
	// 	RegisterCommands(Discord, "1005778938108325970") // just register for the dev guild
	// }
}

func DisconnectDiscord() {
	Discord.Close()
}

func RegisterCommands(s *discordgo.Session, g string) {
	log.Infof("Registering %d/%d commands", 0, len(Commands))

	i := 0
	for _, v := range Commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, g, v.ApplicationCommand)
		if err != nil {
			log.WithError(err).Fatal("Error registering command: " + v.Name)
		}
		i += 1
		log.Infof("Registered %d/%d commands", i, len(Commands))
	}
}
