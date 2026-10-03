package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestHelpAnswersEveryTopicPublicly(t *testing.T) {
	topics := []string{""}
	for _, choice := range helpCommand().Options[0].Choices {
		topics = append(topics, choice.Value.(string))
	}
	for _, topic := range topics {
		i := commandInteraction("interaction-1", helpCommandName)
		if topic != "" {
			i.Data = discordgo.ApplicationCommandInteractionData{Name: helpCommandName, Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "topic", Type: discordgo.ApplicationCommandOptionString, Value: topic},
			}}
		}
		result := help(context.Background(), i)
		data := result.Response.Data
		if result.Task != nil || data.Flags&discordgo.MessageFlagsEphemeral != 0 || data.Content != helpTopics[topic] || !strings.HasPrefix(data.Content, "## ") {
			t.Fatalf("topic %q: %+v", topic, data)
		}
		if len([]rune(data.Content)) > contentLimit {
			t.Fatalf("topic %q is too long for one message", topic)
		}
	}
}
