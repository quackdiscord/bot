package main

import (
	"os"
	"os/signal"

	"github.com/joho/godotenv"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

func init() {
	// load .env file
	if err := godotenv.Load(".env"); err != nil {
		// log.Fatal("No .env.local file found")
		return
	}

	// set the environment
	env := os.Getenv("ENVIORNMENT")

	// log.SetOutput(os.Stdout)
	// log.SetLevel(log.InfoLevel)

	// log.SetFormatter(&log.TextFormatter{
	// 	ForceColors:   true,
	// 	FullTimestamp: true,
	// })

	if env == "dev" {
		log.Warn().Msg("Running in development mode")
	}
}

func main() {
	services.ReadyMessageCache()

	// connect services
	services.ConnectRedis()
	services.ConnectDB()
	services.ConnectKafka()
	services.ConnectDiscord(events.Events)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Info().Msg("Press Ctrl+C to exit")

	// handle shutdown
	<-stop
	log.Warn().Msg("Shutting down")
	services.DisconnectDiscord()
	services.DisconnectDB()
	services.DisconnectRedis()
	services.DisconnectKafka()

	log.Info().Msg("Goodbye!")

}
