package main

import (
	"context"
	"os"

	"github.com/quackdiscord/bot/api"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/discord"
	"github.com/quackdiscord/bot/discord/commands"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func init() {
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Caller().Logger()
	lib.LoadConfig()
}

func main() {
	services.DB.Connect()
	services.Redis.Connect()
	s := storage.New(services.DB.DB, services.Redis.Client)
	if err := s.Migrate(); err != nil {
		log.Fatal().Err(err).Msg("Failed to apply database migrations")
	}

	services.EQ.Init(s)
	services.EQ.Start()

	discord.Connect(s)
	appServices := app.New(s)
	if err := commands.Register(discord.Session, appServices); err != nil {
		log.Error().Err(err).Msg("Failed to register Discord commands")
	}
	if err := app.EnqueuePendingCaseActions(context.Background(), s, 100); err != nil {
		log.Error().Err(err).Msg("Failed to enqueue pending case actions")
	}
	api.Start(s)
}
