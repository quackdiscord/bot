package main

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/services"
	log "github.com/sirupsen/logrus"
)

func init() {
	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)
	// log.SetFormatter(&log.TextFormatter{
	// 	ForceColors:   true,
	// 	FullTimestamp: true,
	// })
	log.SetFormatter(&log.JSONFormatter{})

	// load .env file
	if err := godotenv.Load(".env.local"); err != nil {
		log.Fatal("No .env.local file found")
		return
	}
}

func main() {
	// connect services
	services.ConnectRedis()
	services.ConnectDB()
	services.ConnectDiscord(events.Events)

	// wait until keyboard interrupt
	select {}
}
