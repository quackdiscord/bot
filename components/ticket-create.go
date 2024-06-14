package components

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

func init() {
	Components["create-ticket"] = handleTicketCreate
}

func handleTicketCreate(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {

	// get ticket settings
	tsettings, err := storage.FindTicketSettingsByGuildID(i.GuildID)
	if err != nil {
		log.WithError(err).Error("Failed to get ticket settings")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to get ticket settings. Please try again later.",
		})
	}

	if tsettings == nil {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** This server has not set up the ticket system. Please contact a moderator.",
		})
	}

	if tsettings.ChannelID == "" {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** This server has not set a ticket channel. Please contact a moderator.",
		})
	}

	// make sure the user does not already have a ticket
	currTicket, err := storage.GetUsersTicket(i.Member.User.ID, i.GuildID)
	if err != nil {
		log.WithError(err).Error("Failed to get users ticket")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to get users ticket.",
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
		Name:                fmt.Sprintf("%s's ticket", i.Member.User.Username),
		Invitable:           false,
		AutoArchiveDuration: 0,
		Type:                discordgo.ChannelTypeGuildPrivateThread,
	})
	if err != nil || thread == nil {
		log.WithError(err).Error("Failed to create thread")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to create thread.",
		})
	}

	// invite the user to the thread
	err = s.ThreadMemberAdd(thread.ID, i.Member.User.ID)
	if err != nil {
		log.WithError(err).Error("Failed to invite user to thread")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to invite user to thread.",
		})
	}

	logMsgID := ""
	id, _ := lib.GenID()

	// send a message to the ticket log channel if it exists
	if tsettings.LogChannelID != "" {
		embed := NewEmbed().
			SetDescription(fmt.Sprintf("<@%s> has created a ticket. <#%s>", i.Member.User.ID, thread.ID)).
			SetColor("Green").
			SetTimestamp().
			SetFooter("Ticket ID: " + id).
			MessageEmbed
		msg, err := s.ChannelMessageSendEmbed(tsettings.LogChannelID, embed)
		logMsgID = msg.ID

		if err != nil {
			log.WithError(err).Error("Failed to send message to ticket log channel")
			return ComplexResponse(&discordgo.InteractionResponseData{
				Flags:   discordgo.MessageFlagsEphemeral,
				Content: "<:error:1228053905590718596> **Error:** Failed to send message to ticket log channel.",
			})
		}
	}

	// create a ticket
	t := &structs.Ticket{
		ID:           id,
		GuildID:      i.GuildID,
		OwnerID:      i.Member.User.ID,
		ThreadID:     thread.ID,
		State:        0,
		LogMessageID: string(logMsgID),
	}

	err = storage.CreateTicket(t)
	if err != nil {
		log.WithError(err).Error("Failed to create ticket")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to create ticket.",
		})
	}

	// send a message to the thread
	_, err = s.ChannelMessageSendComplex(thread.ID, &discordgo.MessageSend{
		Content: fmt.Sprintf("<@%s>\n## Welcome to your ticket\n\n> Please explain your issue. A moderator will be here shortly.\n\n`ID: %s`\n<:empty:1250701065591197716>", i.Member.User.ID, id),
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
		log.WithError(err).Error("Failed to send message to thread")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to send message to thread.",
		})
	}

	return ComplexResponse(&discordgo.InteractionResponseData{
		Flags:   discordgo.MessageFlagsEphemeral,
		Content: "Ticket created. <#" + thread.ID + ">",
	})
}
