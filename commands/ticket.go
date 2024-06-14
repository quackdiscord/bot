package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[ticketCmd.Name] = &services.Command{
		ApplicationCommand: ticketCmd,
		Handler:            handleTicket,
	}
}

// /ticket channel <channel> - sets the channel for the ticket system
// /ticket log channel <channel> - sets the channel for the ticket log

var ticketCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "ticket",
	Description:              "Ticket system commands",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		ticketChannelCmd,
		ticketLogChannelCmd,
	},
}

func handleTicket(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "channel":
		return handleTicketChannel(s, i)
	case "log":
		return handleTicketLogChannel(s, i)
	}

	return ContentResponse("oh... this is awkward.", true)
}
