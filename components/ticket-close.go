package components

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
)

func init() {
	Components["close-ticket"] = handleTicketClose
}

func handleTicketClose(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {

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

	// get the ticket
	ticket, err := storage.FindTicketByThreadID(i.ChannelID)
	if err != nil {
		log.WithError(err).Error("Failed to get ticket")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to get ticket. Please try again later.",
		})
	}

	if ticket == nil {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** This ticket does not exist. Please try again later.",
		})
	}

	// close the ticket
	msgs, err := storage.CloseTicket(ticket.ID, ticket.ThreadID, i.Member.User.ID)
	if err != nil {
		log.WithError(err).Error("Failed to close ticket")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to close ticket. Please try again later.",
		})
	}

	// edit the log channel message
	// TODO: add the content of the ticket to the edited embed
	transcript := discordgo.File{
		Name:   "transcript-" + ticket.ID + ".txt",
		Reader: strings.NewReader(*msgs),
	}

	embed := NewEmbed().
		SetDescription(fmt.Sprintf("<@%s>'s ticket has been closed by %s", ticket.OwnerID, i.Member.User.Username)).
		SetColor("Red").
		SetTimestamp().
		SetFooter("Ticket ID: " + ticket.ID).
		MessageEmbed

	_, err = s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel: tsettings.LogChannelID,
		ID:      ticket.LogMessageID,
		Embeds:  &[]*discordgo.MessageEmbed{embed},
		Files:   []*discordgo.File{&transcript},
	})

	if err != nil {
		// Handle error
		fmt.Println("Error editing message:", err)
	}

	// delete the thread
	_, err = s.ChannelDelete(ticket.ThreadID)
	if err != nil {
		log.WithError(err).Error("Failed to delete thread")
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "<:error:1228053905590718596> **Error:** Failed to delete thread. Please try again later.",
		})
	}

	return EmptyResponse()
}
