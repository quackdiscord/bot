package commands

import (
	"errors"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/rs/zerolog/log"
)

func OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	start := time.Now()
	data := i.ApplicationCommandData()

	cmd, ok := services.Commands[data.Name]
	if !ok {
		s.InteractionRespond(i.Interaction, components.ContentResponse(config.Bot.ErrMsgPrefix+"Command does not exist", true))
		return
	}

	resp := cmd.Handler(s, i)
	if resp != nil {
		s.InteractionRespond(i.Interaction, resp)
	} else {
		log.Error().Msgf("Something went wrong while processing a command: %s", i.ApplicationCommandData().Name)
		services.CaptureError(errors.New("Something went wrong while processing a command: " + i.ApplicationCommandData().Name))
		errMessage := config.Bot.ErrMsgPrefix + "Something went wrong while processing the command"
		s.InteractionRespond(i.Interaction, components.ContentResponse(errMessage, true))
		return
	}

	// stop the timer
	end := time.Now()
	lib.CmdRun(s, i, end.Sub(start))
}
