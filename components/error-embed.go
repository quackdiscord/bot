package components

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

func ErrorEmbed(errMessage string) *discordgo.MessageEmbed {
	return NewEmbed().SetDescription(fmt.Sprintf("<:error:1228053905590718596> **Error:** %s", errMessage)).SetColor("Error").MessageEmbed
}
