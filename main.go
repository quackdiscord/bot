package main

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/services"
	"github.com/sirupsen/logrus"
)

func init() {
	logrus.SetOutput(os.Stdout)
	logrus.SetLevel(logrus.InfoLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})
	
	// load .env file
	if err := godotenv.Load(".env.local"); err != nil {
		logrus.Fatal("No .env.local file found")
		return
	}
}

func main() {
	services.ConnectRedis()
	services.ConnectDiscord(events.Events)

	// wait until keyboard interrupt
	select {}
}