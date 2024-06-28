package lib

import (
	"context"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

func CmdRun(s *discordgo.Session, i *discordgo.InteractionCreate, d time.Duration) {
	data := i.ApplicationCommandData()

	// increment the command run counter
	err := services.Redis.HIncrBy(context.Background(), "seeds:cmds", data.Name, 1).Err()
	if err != nil {
		log.Error().AnErr("Failed to increment command run counter", err)
		return
	}
	err = services.Redis.HIncrBy(context.Background(), "seeds:cmds", "total", 1).Err()
	if err != nil {
		log.Error().AnErr("Failed to increment command run counter", err)
		return
	}

	log.Info().Str("command", data.Name).Str("guild", i.GuildID).Str("user", i.Member.User.ID).Str("took", d.String()).Msg("Command executed")
}
