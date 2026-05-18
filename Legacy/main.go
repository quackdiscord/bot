package main

import (
	"io"
	"os"
	"os/signal"

	axiomAdapter "github.com/axiomhq/axiom-go/adapters/zerolog"
	"github.com/joho/godotenv"
	c "github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/events"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

func initLogger() {
	env := os.Getenv("ENVIORNMENT")

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	zerolog.SetGlobalLevel(zerolog.InfoLevel)

	if env == "dev" {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		log.Logger = zerolog.New(os.Stderr).With().Caller().Logger()
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	} else {
		if os.Getenv("AXIOM_TOKEN") == "" {
			log.Logger = zerolog.New(os.Stderr).With().Caller().Logger()
			log.Warn().Msg("Axiom token not set, logging to stderr only")
		} else {
			writer, err := axiomAdapter.New(
				axiomAdapter.SetDataset(os.Getenv("AXIOM_DATASET")),
			)
			if err != nil {
				log.Fatal().Err(err).Msg("Error initializing Axiom adapter")
			}
			log.Logger = zerolog.New(io.MultiWriter(os.Stderr, writer)).With().Caller().Timestamp().Logger()
		}
	}

	log.Info().Msg("Logger initialized")
}

func main() {
	// initialize logger
	initLogger()

	// ready data structures
	services.ReadyMessageCache(c.Bot.MessageCacheSize)
	services.ReadyEventQueue(c.Bot.EventQueueSize)

	// connect services
	services.ConnectRedis()
	services.ConnectDB()
	events.RegisterEvents()
	services.ConnectDiscord(events.Events)
	services.InitSentry()

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
