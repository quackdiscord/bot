package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"golang.org/x/exp/rand"
)

func init() {
	services.Commands[coinflipCmd.Name] = &services.Command{
		ApplicationCommand: coinflipCmd,
		Handler:            handleCoinflip,
	}
}

var coinflipCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "coinflip",
	Description: "Flip a coin!",
}

func handleCoinflip(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	rng := rand.Intn(2)
	if rng == 0 {
		e := components.NewEmbed().
			SetDescription("<:CoinFlipHeads:1320561137221242980> It's **Heads!**").
			SetColor("Yellow").
			MessageEmbed
		return EmbedResponse(e, false)
	}
	e := components.NewEmbed().
		SetDescription("<:CoinFlipTails:1320561138248847411> It's **Tails!**").
		SetColor("White").
		MessageEmbed
	return EmbedResponse(e, false)
}
