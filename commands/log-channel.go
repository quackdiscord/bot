package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var logChannelCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "channel",
	Description: "Set the channel to send logs to.",
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
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to send logs to.",
			Required:    true,
		},
	},
}

func handleLogChannel(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	ltype := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	channel := i.ApplicationCommandData().Options[0].Options[1].ChannelValue(s)

	// make sure the channel is a text channel
	if channel.Type != discordgo.ChannelTypeGuildText {
		return EmbedResponse(components.ErrorEmbed("The channel must be a text channel."), true)
	}

	// get the current log settings
	logSettings, err := storage.FindLogSettingsByID(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get log settings", err)
		return EmbedResponse(components.ErrorEmbed("Failed to get log settings."), true)
	}

	// create a webhook for the given channel
	webhook, err := s.WebhookCreate(channel.ID, "Quack Logging", s.State.User.AvatarURL(""))
	if err != nil {
		log.Error().AnErr("Failed to create webhook", err)
		return EmbedResponse(components.ErrorEmbed("Failed to create webhook."), true)
	}
	whURL := fmt.Sprintf("https://discord.com/api/webhooks/%s/%s", webhook.ID, webhook.Token)

	// if the logSettings object is defined, update it with the new webhook url and channel id
	if logSettings != nil {
		if ltype == "messages" {
			logSettings.MessageChannelID = channel.ID
			logSettings.MessageWebhookURL = whURL
		} else if ltype == "members" {
			logSettings.MemberChannelID = channel.ID
			logSettings.MemberWebhookURL = whURL
		}

		// update the log settings
		err = storage.UpdateLogSettings(logSettings)
		if err != nil {
			log.Error().AnErr("Failed to update log settings", err)
			return EmbedResponse(components.ErrorEmbed("Failed to update log settings."), true)
		}

	} else {
		logSettings = &structs.LogSettings{
			GuildID: i.GuildID,
		}

		if ltype == "messages" {
			logSettings.MessageChannelID = channel.ID
			logSettings.MessageWebhookURL = whURL
			logSettings.MemberChannelID = ""
			logSettings.MemberWebhookURL = ""
		} else if ltype == "members" {
			logSettings.MemberChannelID = channel.ID
			logSettings.MemberWebhookURL = whURL
			logSettings.MessageChannelID = ""
			logSettings.MessageWebhookURL = ""
		}

		// create the log settings object
		err = storage.CreateLogSettings(logSettings)
		if err != nil {
			log.Error().AnErr("Failed to create log settings", err)
			return EmbedResponse(components.ErrorEmbed("Failed to create log settings."), true)
		}
	}

	// send a message to the channel
	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("This channel has been set to log `%s` events.", ltype)).
		SetColor("Main").
		MessageEmbed

	_, err = s.ChannelMessageSendEmbed(channel.ID, embed)
	if err != nil {
		return EmbedResponse(components.ErrorEmbed("Failed to send message to channel."), true)
	}

	embed = components.NewEmbed().
		SetDescription(fmt.Sprintf("Set logging for `%s` in <#%s>.", ltype, channel.ID)).
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
