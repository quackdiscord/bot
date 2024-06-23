package main

import (
	"os"
	"os/signal"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/services"
	log "github.com/sirupsen/logrus"
)

func init() {
	// load .env file
	if err := godotenv.Load(".env"); err != nil {
		// log.Fatal("No .env.local file found")
		return
	}

	// set the environment
	env := os.Getenv("ENVIORNMENT")

	log.SetOutput(os.Stdout)
	log.SetLevel(log.InfoLevel)

	log.SetFormatter(&log.TextFormatter{
		ForceColors:   true,
		FullTimestamp: true,
	})

	if env == "dev" {
		log.Warn("Running in development mode")
	}
}

func main() {
	// connect services
	services.ConnectRedis()
	services.ConnectDB()
	services.ConnectDiscord(events.Events)
	services.ConnectKafka()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Info("Press Ctrl+C to exit")

	// handle shutdown
	<-stop
	log.Warn("Shutting down")
	services.DisconnectDiscord()
	services.DisconnectDB()
	services.DisconnectRedis()
	services.DisconnectKafka()

	log.Info("Goodbye!")

}
