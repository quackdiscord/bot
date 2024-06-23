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

	// make sure the channel is a text channel
	if channel.Type != discordgo.ChannelTypeGuildText {
		return EmbedResponse(components.ErrorEmbed("The channel must be a text channel."), true)
	}

	// Set the ticket channel
	err := storage.SetTicketChannel(i.GuildID, channel.ID)
	if err != nil {
		log.WithError(err).Error("Failed to set ticket channel")
		return EmbedResponse(components.ErrorEmbed("Failed to set ticket channel."), true)
	}

	// send a message to the channel
	str := "# Need Help?\n\n> Click the button below to open a **private ticket**.\n\n<:empty:1250701065591197716>"
	_, err = s.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
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

	if err != nil {
		log.WithError(err).Error("Failed to send message to ticket channel")
		return EmbedResponse(components.ErrorEmbed("Failed to send message to ticket channel."), true)
	}

	embed := components.NewEmbed().
		SetDescription("Ticket channel set to <#" + channel.ID + ">.\n\nChange the permissions so only bots can send messages and so your moderators can manage threads.").
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
