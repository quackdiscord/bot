package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

var ticketQueueCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "queue",
	Description: "Get the queue of tickets",
}

func handleTicketQueue(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)

	// get the queue
	queue, err := storage.GetOpenTickets(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to get ticket queue", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to get ticket queue."), true)
	}

	// if the queue is empty, return an error
	if len(queue) == 0 {
		embed := components.NewEmbed().
			SetDescription("There are no tickets in the queue.").
			SetColor("Main").
			MessageEmbed
		return components.EmbedResponse(embed, false)
	}

	// if the queue is not empty, return the queue
	c := fmt.Sprintf("**%d** tickets in the queue\n\n", len(queue))
	for _, t := range queue {
		parsedTime, _ := time.Parse("2006-01-02 15:04:05", t.CreatedAt)
		unixTime := parsedTime.Unix()

		c += fmt.Sprintf("<t:%d:R> <@%s>'s ticket\n", unixTime, t.OwnerID)
		c += fmt.Sprintf("<:text2:1229344477131309136> <#%s>\n", t.ThreadID)
		c += fmt.Sprintf("<:text:1229343822337802271> `ID: %s`\n", t.ID)
		c += "\n"
	}

	embed := components.NewEmbed().
		SetDescription(c).
		SetTimestamp().
		SetAuthor("Ticket Queue", guild.IconURL("")).
		SetColor("Main").
		MessageEmbed

	return components.EmbedResponse(embed, false)
}
