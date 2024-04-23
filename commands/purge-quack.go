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
			Required: 	 true,
			MaxValue: 100,
		},
		{
			Type: 		 discordgo.ApplicationCommandOptionChannel,
			Name: 		 "channel",
			Description: "The channel to purge messages from",
			Required: 	 false,
		},
	},
}

func handlePurgeQuack(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	amount := i.ApplicationCommandData().Options[0].Options[0].IntValue()
	channel := i.ChannelID

	go func(){
		if len(i.ApplicationCommandData().Options[0].Options) > 1 {
			channel = i.ApplicationCommandData().Options[0].Options[1].ChannelValue(s).ID
		}

		msgs, err := s.ChannelMessages(channel, int(amount), "", "", "")
		if err != nil {
			log.WithError(err).Error("Failed to fetch messages for purge")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch messages.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		msgIds := make([]string, len(msgs))
		for i, msg := range msgs {
			if msg.Author.ID == s.State.User.ID {
				msgIds[i] = msg.ID
			}
		}

		if len(msgIds) == 0 {
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** There are no messages to delete.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

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
			SetDescription(fmt.Sprintf("Successfully purged `%d` messages from <@%s> in <#%s>.", len(msgIds), s.State.User.ID, channel)).
			SetColor("Main").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}
