package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[helpCmd.Name] = &services.Command{
		ApplicationCommand: helpCmd,
		Handler:            handleHelp,
	}
}

var helpCmd = &discordgo.ApplicationCommand{
	Type:        discordgo.ChatApplicationCommand,
	Name:        "help",
	Description: "Get some help :)",
}

func handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	embed := components.NewEmbed().
		SetTitle("Quack Help").
		SetDescription("All of Quack's commands use the `/` prefix.\n\n**Commands**: All of Quack's commands can be found [here](https://quackbot.xyz/commands)\n**Need Help?** [Join our Support Server](https://discord.gg/hUsR6fRYyE)").
		AddField("Links", "[🌐 Website](https://quackbot.xyz) | [<:invite:823987169978613851> Invite](https://quackbot.xyz/invite) | [<:discord:823989269626355793> Need Help?](https://quackbot.xyz/discord)").
		SetThumbnail("https://www.quackbot.xyz/images/png/logo.png").
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
