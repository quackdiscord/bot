package services

import (
	"os"

	"github.com/bwmarrin/discordgo"
	"github.com/sirupsen/logrus"
)

var Discord *discordgo.Session

func ConnectDiscord(events []interface{}) {
	Discord, _ = discordgo.New(os.Getenv("DEV_TOKEN"))
	Discord.Identify.Intents = discordgo.IntentsGuildMessages
	
	for _, h := range events {
		Discord.AddHandler(h)
	}

	err := Discord.Open()
	if err != nil {
		logrus.WithError(err).Fatal("Error opening connection to Discord")
	}
}