package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[pingCmd.Name] = &services.Command{
		ApplicationCommand: pingCmd,
		Handler:            handlePing,
	}
}

var pingCmd = &discordgo.ApplicationCommand{
	Type: discordgo.ChatApplicationCommand,
	Name: "ping",
	Description: "Ping the bot and get the latency",
}

func handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	HeartbeatLatency := s.HeartbeatLatency().Milliseconds()

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("**🏓 Pong ** - Latency: `%dms`", HeartbeatLatency)).
		SetColor("Green").
		MessageEmbed

	return EmbedResponse(embed, false)
}
