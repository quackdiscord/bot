package components

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/storage"
)

func init() {
	Components["close-ticket"] = handleTicketClose
}

func handleTicketClose(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, err := s.Guild(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get guild", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to get guild. Please try again later.",
		})
	}

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

	// get the ticket
	ticket, err := storage.FindTicketByThreadID(i.ChannelID)
	if err != nil {
		log.Error().AnErr("Failed to get ticket", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to get ticket. Please try again later.",
		})
	}

	if ticket == nil {
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "This ticket does not exist. Please try again later.",
		})
	}

	// get the user and the owner
	// the user is the person who clicked the button the owner is the person who created the ticket
	// they may be the same they may be different
	var user *discordgo.User
	if i.Member != nil {
		user = i.Member.User
	} else {
		user = i.User
	}

	owner, _ := s.GuildMember(i.GuildID, ticket.OwnerID)
	// if owner is nil, that means the user is not in the guild so use the person who clicked the button
	if owner == nil {
		owner = i.Member
	}

	// close the ticket
	msgs, err := storage.CloseTicket(ticket.ID, ticket.ThreadID, user.ID)
	if err != nil {
		log.Error().AnErr("Failed to close ticket", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to close ticket. Please try again later.",
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
		log.Error().AnErr("Failed to edit message", err)
	}

	// attempt to dm the owner of the ticket
	dmChanel, err := s.UserChannelCreate(ticket.OwnerID)
	if err != nil {
		log.Error().AnErr("Failed to DM owner of ticket", err)
	} else {
		transcript = discordgo.File{
			Name:   "transcript-" + ticket.ID + ".txt",
			Reader: strings.NewReader(*msgs),
		}

		embed = NewEmbed().
			SetDescription(fmt.Sprintf("Your ticket `%s` has been closed.\n\nIf you need more help, please open a new ticket!\n\n*A transcript of the conversation has been attached.*", ticket.ID)).
			SetColor("Main").
			SetAuthor(guild.Name, guild.IconURL("")).
			SetTimestamp().
			MessageEmbed

		_, err = s.ChannelMessageSendComplex(dmChanel.ID, &discordgo.MessageSend{
			Embeds: []*discordgo.MessageEmbed{embed},
			Files:  []*discordgo.File{&transcript},
		})

		if err != nil {
			log.Error().AnErr("Failed to send DM to owner of ticket", err)
		}
	}

	// delete the thread
	_, err = s.ChannelDelete(ticket.ThreadID)
	if err != nil {
		log.Error().AnErr("Failed to delete thread", err)
		return ComplexResponse(&discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: config.Bot.ErrMsgPrefix + "Failed to delete thread. Please try again later.",
		})
	}

	log.Info().Str("guild", i.GuildID).Str("user", user.ID).Str("ticket", ticket.ID).Msg("Ticket closed")

	return EmptyResponse()
}
