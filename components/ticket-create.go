package components

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

func init() {
	Components["create-ticket"] = handleTicketCreate
}

func handleTicketCreate(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.Member.User

	// get ticket settings
	tsettings, err := storage.FindTicketSettingsByGuildID(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get ticket settings", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to get ticket settings. Please try again later.",
		})
	}

	if tsettings == nil {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "This server has not set up the ticket system. Please contact a moderator.",
		})
	}

	if tsettings.ChannelID == "" {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "This server has not set a ticket channel. Please contact a moderator.",
		})
	}

	// make sure the user does not already have a ticket
	currTicket, err := storage.GetUsersTicket(user.ID, i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get users ticket", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to get users ticket.",
		})
	}
	if currTicket != nil {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "You already have a ticket open. <#" + *currTicket + ">",
		})
	}

	// create a thread
	thread, err := s.ThreadStartComplex(tsettings.ChannelID, &discordgo.ThreadStart{
		Name:                fmt.Sprintf("%s's ticket", user.Username),
		Invitable:           false,
		AutoArchiveDuration: 0,
		Type:                discordgo.ChannelTypeGuildPrivateThread,
	})
	if err != nil || thread == nil {
		log.Error().AnErr("Failed to create thread", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to create thread.",
		})
	}

	logMsgID := ""
	id, _ := lib.GenID()

	// send a message to the ticket log channel if it exists
	if tsettings.LogChannelID != "" {
		embed := NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has opened a ticket. <#%s>", user.ID, thread.ID)).
			SetColor("Green").
			SetAuthor("Ticket Opened", user.AvatarURL("")).
			SetTimestamp().
			SetFooter("Ticket ID: " + id).
			MessageEmbed
		msg, err := s.ChannelMessageSendEmbed(tsettings.LogChannelID, embed)
		logMsgID = msg.ID

		if err != nil {
			log.Error().AnErr("Failed to send message to ticket log channel", err)
			return ComplexResponse(&discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: config.Bot.ErrMsgPrefix + "Failed to send message to ticket log channel.",
			})
		}
	}

	// create a ticket
	t := &structs.Ticket{
		ID:           id,
		GuildID:      i.GuildID,
		OwnerID:      user.ID,
		ThreadID:     thread.ID,
		State:        0,
		LogMessageID: string(logMsgID),
	}

	err = storage.CreateTicket(t)
	if err != nil {
		log.Error().AnErr("Failed to create ticket", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to create ticket.",
		})
	}

	// send a message to the thread
	_, err = s.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{
		Content: fmt.Sprintf("<@%s>\n# Welcome to your ticket\n\n> Please explain your issue. A moderator will be here shortly.\n\nTicket ID: `%s`\n<:empty:1250701065591197716>", user.ID, id),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.Button{
						CustomID: "close-ticket",
						Label:    "Close Ticket",
						Style:    discordgo.DangerButton,
					},
				},
			},
		},
	})

	if err != nil {
		log.Error().AnErr("Failed to send message to thread", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to send message to thread.",
		})
	}

	log.Info().Str("guild", i.GuildID).Str("user", user.ID).Str("ticket", id).Msg("Ticket created")

	return ComplexResponse(&discordgo.InteractionResponseData{
		Flags:   discordgo.MessageFlagsEphemeral,
		Content: "> Ticket created! <#" + thread.ID + ">",
	})
}
