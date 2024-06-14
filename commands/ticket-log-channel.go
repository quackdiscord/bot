package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
)

var ticketLogChannelCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "log",
	Description: "Manage ticket logs.",
	Options: []*discordgo.ApplicationCommandOption{
		{
			// subcommand
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "channel",
			Description: "Set the channel to send ticket logs to.",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "The channel to send ticket logs to.",
					Required:    true,
				},
			},
		},
	},
}

func handleTicketLogChannel(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	channel := i.ApplicationCommandData().Options[0].Options[0].Options[0].ChannelValue(s)

	go func() {
		// make sure the channel is a text channel
		if channel.Type != discordgo.ChannelTypeGuildText {
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** The channel must be a text channel.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// Set the ticket log channel
		err := storage.SetTicketLogChannel(i.GuildID, channel.ID)
		if err != nil {
			log.WithError(err).Error("Failed to set ticket log channel")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to set ticket log channel.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription("Ticket log channel set to <#" + channel.ID + ">.").
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

		// send a message to the channel
		embed = components.NewEmbed().
			SetDescription("Ticket logs will be sent here.").
			SetColor("Main").
			MessageEmbed

		_, err = s.ChannelMessageSendEmbed(channel.ID, embed)
		if err != nil {
			return
		}
	}()

	return LoadingResponse()
}
