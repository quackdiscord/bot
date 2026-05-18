package commands

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/rs/zerolog/log"
)

var purgeContainsCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "contains",
	Description: "Purge specified amount of message with a specific string in a channel",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "string",
			Description: "The string to search for",
			Required:    true,
			MaxValue:    1,
		},
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

func handlePurgeContains(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	amount := i.ApplicationCommandData().Options[0].Options[1].IntValue()
	str := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	channel := i.ChannelID

	go func() {
		if len(i.ApplicationCommandData().Options[0].Options) > 2 {
			channel = i.ApplicationCommandData().Options[0].Options[2].ChannelValue(s).ID
		}

		// fetch the past 100 messages (discord limit)
		msgs, err := s.ChannelMessages(channel, 100, "", "", "")
		if err != nil {
			log.Error().AnErr("Failed to fetch messages for purge", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch messages.")},
			})
			return
		}

		// make a list of message ids to delete
		msgIds := make([]string, 0)
		for _, msg := range msgs {
			// if the message.Content contains the emoji string, add it to the list
			if strings.Contains(msg.Content, str) && len(msgIds) < int(amount) {
				msgIds = append(msgIds, msg.ID)
			}
		}

		if len(msgIds) == 0 {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("No messages found to purge.")},
			})
			return
		}

		// delete the messages
		err = s.ChannelMessagesBulkDelete(channel, msgIds)
		if err != nil {
			log.Error().AnErr("Failed to delete messages", err)
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete messages.")},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("Successfully purged `%d` messages containing `%s` in <#%s>.", len(msgIds), str, channel)).
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return components.LoadingResponse()
}
