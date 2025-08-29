package main

import (
	"os"
	"os/signal"

	"github.com/joho/godotenv"
	c "github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	if err := godotenv.Load(".env"); err != nil {
		return
	}

	env := os.Getenv("ENVIORNMENT")

	if env == "dev" {
		log.Warn().Msg("Running in development mode")
	}
}

func main() {
	// ready data structures
	services.ReadyMessageCache(c.Bot.MessageCacheSize)
	services.ReadyEventQueue(c.Bot.EventQueueSize)

	// connect services
	services.ConnectRedis()
	services.ConnectDB()
	events.RegisterEvents()
	services.ConnectDiscord(events.Events)

	// start the event queue
	go services.EQ.Start(c.Bot.EventQueueWorkers)

	// register stats collector and start the cron scheduler
	services.RegisterStatsCollector(utils.CollectAndSaveStats)
	services.StartCron(services.Discord)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	log.Info().Msg("Press Ctrl+C to exit")

	// handle shutdown
	<-stop
	log.Warn().Msg("Shutting down")
	services.StopCron()
	services.DisconnectDiscord()
	services.DisconnectDB()
	services.DisconnectRedis()
	services.EQ.Stop()

	log.Info().Msg("Goodbye!")

}
