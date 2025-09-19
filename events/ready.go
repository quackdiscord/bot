package events

import (
	"math/rand"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onReady)
}

func onReady(s *discordgo.Session, r *discordgo.Ready) {
	// activities
	actvs := []string{
		"Making Discord safer.",
		"Easy moderation for everyone.",
		"Keeping this server safe.",
		"Moderation, logging, tickets, & more",
		"Quackbot.xyz",
		"Quackbot.xyz/discord",
		"Quackbot.xyz/invite",
		"Quackbot.xyz/commands",
		"Swimming in the pond",
		"*Duck noises*",
		"Getting rid of bad eggs",
		"Formerly Seeds",
		"Eating bread",
		"Give me some bread",
		"Watching you",
		"Leading the ducklings",
	}

	s.UpdateCustomStatus(actvs[rand.Intn(len(actvs))])

	log.Info().Msgf("Signed in as %s", s.State.User.String())

	// every 10 minutes, rotate the activity
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			s.UpdateCustomStatus(actvs[rand.Intn(len(actvs))])
		}
	}()
}
