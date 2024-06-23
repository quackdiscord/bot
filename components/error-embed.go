package components

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
)

func ErrorEmbed(errMessage string) *discordgo.MessageEmbed {
	return NewEmbed().SetDescription(fmt.Sprintf("%s%s", config.Bot.ErrMsgPrefix, errMessage)).SetColor("Error").MessageEmbed
}
