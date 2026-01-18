package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var logDisableCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "disable",
	Description: "Disable logging for a specific log type.",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "type",
			Description: "The type of log to set the channel for.",
			Required:    true,
			Choices: []*discordgo.ApplicationCommandOptionChoice{
				{
					Name:  "messages",
					Value: "messages",
				},
				{
					Name:  "members",
					Value: "members",
				},
			},
		},
	},
}

func handleLogDisable(i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	ltype := i.ApplicationCommandData().Options[0].Options[0].StringValue()

	// get the current log settings
	logSettings, err := storage.FindLogSettingsByID(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get log settings", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to get log settings."), true)
	}

	// if the logSettings object is defined, update it with the new webhook url and channel id
	if logSettings != nil {
		if ltype == "messages" {
			logSettings.MessageChannelID = ""
			logSettings.MessageWebhookURL = ""
		} else if ltype == "members" {
			logSettings.MemberChannelID = ""
			logSettings.MemberWebhookURL = ""
		}

		// update the log settings
		err = storage.UpdateLogSettings(logSettings)
		if err != nil {
			log.Error().AnErr("Failed to update log settings", err)
			services.CaptureError(err)
			return components.EmbedResponse(components.ErrorEmbed("Failed to update log settings."), true)
		}

	} else {
		logSettings = &structs.LogSettings{
			GuildID: i.GuildID,
		}

		if ltype == "messages" {
			logSettings.MessageChannelID = ""
			logSettings.MessageWebhookURL = ""
			logSettings.MemberChannelID = ""
			logSettings.MemberWebhookURL = ""
		} else if ltype == "members" {
			logSettings.MemberChannelID = ""
			logSettings.MemberWebhookURL = ""
			logSettings.MessageChannelID = ""
			logSettings.MessageWebhookURL = ""
		}

		// create the log settings object
		err = storage.CreateLogSettings(logSettings)
		if err != nil {
			log.Error().AnErr("Failed to create log settings", err)
			services.CaptureError(err)
			return components.EmbedResponse(components.ErrorEmbed("Failed to create log settings."), true)
		}
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("Disabled logging for `%s` events", ltype)).
		SetColor("Main").
		MessageEmbed

	return components.EmbedResponse(embed, false)
}
