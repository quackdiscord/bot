package commands

import (
	"database/sql"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var honeypotCreateCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "create",
	Description: "Create a new honeypot channel",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "message",
			Description: "A custom message to display in the honeypot channel.",
			Required:    false,
		},
	},
}

func handleHoneypotCreate(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	subCmdOptions := i.ApplicationCommandData().Options[0].Options
	msg := ""
	for _, opt := range subCmdOptions {
		if opt.Name == "message" {
			msg = opt.StringValue()
		}
	}

	// create the channel
	channel, err := s.GuildChannelCreate(i.GuildID, "honeypot", discordgo.ChannelTypeGuildText)
	if err != nil {
		log.Error().AnErr("Failed to create honeypot channel", err)
		services.CaptureError(err)
		return EmbedResponse(components.ErrorEmbed("Failed to create honeypot channel."), true)
	}

	var message sql.NullString
	if msg != "" {
		// parse the message for new lines and replace them with real new lines so the bot sends the message correctly
		msg = strings.ReplaceAll(msg, "\\n", "\n")
		message = sql.NullString{String: msg, Valid: true}
	} else {
		message = sql.NullString{String: "# Warning!\n\nThis is a honeypot channel meant to catch scammers.\n\n> Do not message here, you will be banned.", Valid: true}
	}

	// send the message to the channel
	sentMsg, err := s.ChannelMessageSend(channel.ID, message.String+"\n\n-# <:ban:1165590688554033183> Banned **0** users so far.")
	if err != nil {
		log.Error().AnErr("Failed to send message to honeypot channel", err)
		services.CaptureError(err)
		return EmbedResponse(components.ErrorEmbed("Failed to send message to honeypot channel."), true)
	}

	honeypot := &structs.Honeypot{
		ID:           channel.ID,
		GuildID:      i.GuildID,
		Action:       "ban",
		Message:      message,
		MessageID:    sentMsg.ID,
		ActionsTaken: 0,
	}

	err = storage.CreateHoneypot(honeypot)
	if err != nil {
		log.Error().AnErr("Failed to create honeypot", err)
		services.CaptureError(err)
		return EmbedResponse(components.ErrorEmbed("Failed to create honeypot."), true)
	}

	embed := components.NewEmbed().
		SetDescription("Honeypot channel created successfully.\n*Feel free to change the name of the channel to your liking.*").
		SetColor("Main").
		MessageEmbed

	return EmbedResponse(embed, false)
}
