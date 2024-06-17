package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	log "github.com/sirupsen/logrus"
)

var purgeEmbedsCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "embeds",
	Description: "Purge specified amount of embed messages from a channel",
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

func handlePurgeEmbeds(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	amount := i.ApplicationCommandData().Options[0].Options[0].IntValue()
	channel := i.ChannelID

	go func() {
		if len(i.ApplicationCommandData().Options[0].Options) > 1 {
			channel = i.ApplicationCommandData().Options[0].Options[1].ChannelValue(s).ID
		}

		// fetch the past x messages (x = amount)
		msgs, err := s.ChannelMessages(channel, int(amount), "", "", "")
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
			// at the message id to the list if its from the user, and we havent reached the limit yet
			if len(msg.Embeds) > 0 && len(msgIds) < int(amount) {
				msgIds = append(msgIds, msg.ID)
			}
		}

		// if there are no messages to delete, return an error
		if len(msgIds) == 0 {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("There are no messages to delete.")},
			})
			return
		}

		// delete the messages
		err2 := s.ChannelMessagesBulkDelete(channel, msgIds)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete messages")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete messages.")},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("Successfully purged `%d` messages with embeds in <#%s>.", len(msgIds), channel)).
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}
