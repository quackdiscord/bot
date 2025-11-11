package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
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

	// make sure the channel is a text channel
	if channel.Type != discordgo.ChannelTypeGuildText {
		return EmbedResponse(components.ErrorEmbed("The channel must be a text channel."), true)
	}

	// Set the ticket log channel
	err := storage.SetTicketLogChannel(i.GuildID, channel.ID)
	if err != nil {
		log.Error().AnErr("Failed to set ticket log channel", err)
		services.CaptureError(err)
		return EmbedResponse(components.ErrorEmbed("Failed to set ticket log channel."), true)
	}

	// send a message to the channel
	embed := components.NewEmbed().
		SetDescription("Ticket logs will be sent here.").
		SetColor("Main").
		MessageEmbed

	_, err = s.ChannelMessageSendEmbed(channel.ID, embed)
	if err != nil {
		return EmbedResponse(components.ErrorEmbed("Failed to send message to ticket log channel."), true)
	}

	embed = components.NewEmbed().
		SetDescription("Ticket log channel set to <#" + channel.ID + ">.").
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
