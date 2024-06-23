package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
)

var ticketChannelCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "channel",
	Description: "Set the channel the ticket message will be sent to.",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to send the ticket message to.",
			Required:    true,
		},
	},
}

func handleTicketChannel(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	channel := i.ApplicationCommandData().Options[0].Options[0].ChannelValue(s)

	go func() {
		// make sure the channel is a text channel
		if channel.Type != discordgo.ChannelTypeGuildText {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("The channel must be a text channel.")},
			})
			return
		}

		// Set the ticket channel
		err := storage.SetTicketChannel(i.GuildID, channel.ID)
		if err != nil {
			log.WithError(err).Error("Failed to set ticket channel")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to set ticket channel.")},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription("Ticket channel set to <#" + channel.ID + ">.\n\nChange the permissions so only bots can send messages and so your moderators can manage threads.").
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

		// send a message to the channel
		str := "# Need Help?\n\n> Click the button below to open a **private ticket**.\n\n<:empty:1250701065591197716>"
		_, err2 := s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
			Content: str,
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.Button{
							CustomID: "create-ticket",
							Label:    "Open a ticket",
							Style:    discordgo.PrimaryButton,
						},
					},
				},
			},
		})

		if err2 != nil {
			log.WithError(err2).Error("Failed to send message to ticket channel")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to send message to ticket channel.")},
			})
			return
		}

	}()

	return LoadingResponse()
}
