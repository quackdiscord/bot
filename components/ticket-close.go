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

	// get the user and the owner
	// the user is the person who clicked the button the owner is the person who created the ticket
	// they may be the same they may be different
	owner, _ := s.GuildMember(i.GuildID, ticket.OwnerID)
	user := i.Member.User

	// close the ticket
	msgs, err := storage.CloseTicket(ticket.ID, ticket.ThreadID, user.ID)
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
		SetDescription(fmt.Sprintf("<@%s>'s ticket has been closed by %s", ticket.OwnerID, user.Username)).
		SetColor("Red").
		SetAuthor("Ticket Closed", owner.AvatarURL("")).
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
		log.WithError(err).Error("Failed to edit message")
	}

	log.WithFields(
		log.Fields{
			"guild":  i.GuildID,
			"user":   user.ID,
			"ticket": ticket.ID,
		},
	).Info("Ticket closed")

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
