package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	log "github.com/sirupsen/logrus"
)

var purgeBotsCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "bots",
	Description: "Purge specified amount of message from bots in a channel",
	Options:     []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionInteger,
			Name:        "amount",
			Description: "The amount of messages to purge",
			Required: 	 true,
			MaxValue:    100,
		},
		{
			Type: 		 discordgo.ApplicationCommandOptionChannel,
			Name: 		 "channel",
			Description: "The channel to purge messages from",
			Required: 	 false,
		},
	},
}

func handlePurgeBots(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	amount := i.ApplicationCommandData().Options[0].Options[0].IntValue()
	channel := i.ChannelID

	go func(){
		if len(i.ApplicationCommandData().Options[0].Options) > 2 {
			channel = i.ApplicationCommandData().Options[0].Options[2].ChannelValue(s).ID
		}

		// fetch the past 100 messages (discord limit)
		msgs, err := s.ChannelMessages(channel, 100, "", "", "")
		if err != nil {
			log.WithError(err).Error("Failed to fetch messages for purge")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch messages.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// make a list of message ids to delete
		msgIds := make([]string, 0)
		for _, msg := range msgs {
			// at the message id to the list if its from the user, and we havent reached the limit yet
			if msg.Author.Bot && len(msgIds) < int(amount) {
				msgIds = append(msgIds, msg.ID)
			}
		}

		if len(msgIds) == 0 {
			embed := components.NewEmbed().SetDescription("No messages found to purge.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// delete the messages
		err2 := s.ChannelMessagesBulkDelete(channel, msgIds)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete messages")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to delete messages.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("Successfully purged `%d` messages from bots in <#%s>.", len(msgIds), channel)).
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}
