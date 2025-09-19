package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var appealsSetupCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "setup",
	Description: "Setup the appeals system",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "Channel to send appeal logs to",
			Required:    true,
		},
	},
}

func handleAppealsSetup(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	data := i.ApplicationCommandData().Options[0]
	msg := "What would you like to say to the moderators?"
	channel := data.Options[0].ChannelValue(s)

	if channel.Type != discordgo.ChannelTypeGuildText {
		return EmbedResponse(components.ErrorEmbed("The channel must be a text channel."), true)
	}

	settings := &structs.AppealSettings{
		GuildID:   i.GuildID,
		Message:   msg,
		ChannelID: channel.ID,
	}

	if err := storage.UpsertAppealSettings(settings); err != nil {
		log.Error().AnErr("Failed to save appeal settings", err)
		return EmbedResponse(components.ErrorEmbed("Failed to save appeal settings."), true)
	}

	embed := components.NewEmbed().
		SetDescription("Appeals have been configured.").
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
