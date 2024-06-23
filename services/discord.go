package services

import (
	"os"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	log "github.com/sirupsen/logrus"
)

var Discord *discordgo.Session
var Commands = make(map[string]*Command)
var RegisteredCommands = make([]*discordgo.ApplicationCommand, len(Commands))

const MaxMessageCacheSize = 10_000

var (
	MessageCache = make(map[string]*discordgo.Message)
	CacheOrder   []string
	CacheMutex   sync.Mutex
)

type Command struct {
	*discordgo.ApplicationCommand
	Handler func(*discordgo.Session, *discordgo.InteractionCreate) *discordgo.InteractionResponse
}

func ConnectDiscord(events []interface{}) {
	token := ""
	env := os.Getenv("ENVIORNMENT")
	registerCmds := os.Getenv("REGISTER_CMDS")

	if env == "prod" {
		token = os.Getenv("TOKEN")
	} else {
		token = os.Getenv("DEV_TOKEN")
	}

	Discord, _ = discordgo.New(token)
	Discord.Identify.Intents = discordgo.Intent(3276543) // all unpriveledged intents + message content + guild members
	Discord.State.MaxMessageCount = MaxMessageCacheSize

	for i, h := range events {
		Discord.AddHandler(h)
		log.Infof("Added %d/%d event handlers", i+1, len(events))
	}

	err := Discord.Open()
	if err != nil {
		log.WithError(err).Fatal("Error opening connection to Discord")
	}

	// register commands
	if registerCmds == "true" {
		if env == "prod" {
			log.Infof("Registering %d global commands", len(Commands))
			RegisterCommands(Discord, "") // register globally
		} else {
			log.Infof("Registering %d dev commands", len(Commands))
			RegisterCommands(Discord, config.Bot.DevGuildID) // just register for the dev guild
		}
	}
}

func RegisterCommands(s *discordgo.Session, g string) {
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

func DisconnectDiscord() {
	Discord.Close()
	log.Info("Disconnected from Discord")
}
