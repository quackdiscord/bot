package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
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
		result := help(quack.DashboardLinks{})(context.Background(), i)
		data := result.Response.Data
		if result.Task != nil || data.Flags&discordgo.MessageFlagsEphemeral != 0 || data.Content != helpTopics[topic] || !strings.HasPrefix(data.Content, "## ") {
			t.Fatalf("topic %q: %+v", topic, data)
		}
		if len([]rune(data.Content)) > contentLimit {
			t.Fatalf("topic %q is too long for one message", topic)
		}
	}
}

// TestHelpLinksTheTopicsDashboardPage links each topic to its staff page
// in a server, and leaves the link out in DMs.
func TestHelpLinksTheTopicsDashboardPage(t *testing.T) {
	handler := help(quack.NewDashboardLinks("http://localhost:3000"))
	for topic, want := range map[string]string{
		"":        "http://localhost:3000/guilds/guild-1",
		"rules":   "http://localhost:3000/guilds/guild-1/rules",
		"cases":   "http://localhost:3000/guilds/guild-1/cases",
		"appeals": "http://localhost:3000/guilds/guild-1/appeals",
		"setup":   "http://localhost:3000/guilds/guild-1/settings",
	} {
		i := commandInteraction("interaction-1", helpCommandName)
		if topic != "" {
			i.Data = discordgo.ApplicationCommandInteractionData{Name: helpCommandName, Options: []*discordgo.ApplicationCommandInteractionDataOption{
				{Name: "topic", Type: discordgo.ApplicationCommandOptionString, Value: topic},
			}}
		}
		data := handler(context.Background(), i).Response.Data
		button := data.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
		if button.URL != want || button.Label != "Open in dashboard" {
			t.Fatalf("topic %q: %+v", topic, button)
		}
	}
	i := commandInteraction("interaction-2", helpCommandName)
	i.GuildID = ""
	if data := handler(context.Background(), i).Response.Data; len(data.Components) != 0 {
		t.Fatalf("DM help linked a guild page: %+v", data.Components)
	}
}
