package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	log "github.com/sirupsen/logrus"
)

var purgeQuackCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "quack",
	Description: "Purge specified amount of messages from Quack in a channel",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionInteger,
			Name:        "amount",
			Description: "The amount of messages to purge",
			Required:    true,
			MaxValue:    100,
		},
		{
			Type:        discordgo.ApplicationCommandOptionChannel,
			Name:        "channel",
			Description: "The channel to purge messages from",
			Required:    false,
		},
	},
}

func handlePurgeQuack(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	amount := i.ApplicationCommandData().Options[0].Options[0].IntValue()
	channel := i.ChannelID

	go func() {
		if len(i.ApplicationCommandData().Options[0].Options) > 1 {
			channel = i.ApplicationCommandData().Options[0].Options[1].ChannelValue(s).ID
		}

		// fetch the past 100 messages (discord limit)
		msgs, err := s.ChannelMessages(channel, 100, "", "", "")
		if err != nil {
			log.WithError(err).Error("Failed to fetch messages for purge")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch messages.")},
			})
			return
		}

		// make a list of message ids to delete
		msgIds := make([]string, 0)
		for _, msg := range msgs {
			// at the message id to the list if its from the bot, and we havent reached the limit yet
			if msg.Author.ID == s.State.User.ID && len(msgIds) < int(amount) {
				msgIds = append(msgIds, msg.ID)
			}
		}

		// if no messages were found, return an error
		if len(msgIds) == 0 {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("No messages found to purge.")},
			})
			return
		}

		// attempt to bulk delete the messages
		err2 := s.ChannelMessagesBulkDelete(channel, msgIds)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete messages")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete messages.")},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("Successfully purged `%d` messages from <@%s> in <#%s>.", len(msgIds), s.State.User.ID, channel)).
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}
