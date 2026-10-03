package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// TestCommandMentionsResolveNativePaths covers code and prose references,
// keeping arguments, member literals, and each application's IDs apart.
func TestCommandMentionsResolveNativePaths(t *testing.T) {
	app := "mentions-test"
	SetCommandMentions(app, []*discordgo.ApplicationCommand{{ID: "123", Name: "case", Options: []*discordgo.ApplicationCommandOption{
		{Name: "add", Type: discordgo.ApplicationCommandOptionSubCommand},
		{Name: "view", Type: discordgo.ApplicationCommandOptionSubCommand},
		{Name: "history", Type: discordgo.ApplicationCommandOptionSubCommandGroup, Options: []*discordgo.ApplicationCommandOption{
			{Name: "list", Type: discordgo.ApplicationCommandOptionSubCommand},
		}},
	}}, {ID: "456", Name: "help"}, {ID: "789", Name: "moderate", Type: discordgo.UserApplicationCommand}})
	for _, item := range []struct{ input, want string }{
		{"Use /case add.", "Use </case add:123>."},
		{"Use `/case view case:42`.", "Use </case view:123> `case:42`."},
		{"Try `/case history list` or /help.", "Try </case history list:123> or </help:456>."},
		{"/case viewer /case history /unknown /moderate", "/case viewer /case history /unknown /moderate"},
		{"https://example.com/case/add </case add:123>", "https://example.com/case/add </case add:123>"},
		{"\\`/case add\\`", "\\`/case add\\`"},
		{"```\n/case add\n```", "```\n/case add\n```"},
		{"`user mentioned /case add`", "`user mentioned /case add`"},
		{"/case/add", "/case/add"},
		{"/helpé /help界 /help١ /help\u0301", "/helpé /help界 /help١ /help\u0301"},
		{"`/helpé` and /help。", "`/helpé` and </help:456>。"},
	} {
		if got := ResolveCommandMentions(item.input, app); got != item.want {
			t.Errorf("%q => %q, want %q", item.input, got, item.want)
		}
		if got := ResolveCommandMentions(item.input, "other-app"); got != item.input {
			t.Fatal("cross-application command ID leak", got)
		}
	}
}

// TestCommandMentionRegistryRefresh drops stale subcommands and IDs across
// syncs.
func TestCommandMentionRegistryRefresh(t *testing.T) {
	app := "refresh-test"
	command := &discordgo.ApplicationCommand{Name: "case", Options: []*discordgo.ApplicationCommandOption{
		{Name: "old", Type: discordgo.ApplicationCommandOptionSubCommand},
	}}
	RegisterCommandMentions(app, command, "old-id")
	command.Options[0].Name = "new"
	RegisterCommandMentions(app, command, "new-id")
	if got := ResolveCommandMentions("/case old /case new", app); got != "/case old </case new:new-id>" {
		t.Fatal(got)
	}
	RemoveCommandMentions(app, "case")
	if got := ResolveCommandMentions("/case new", app); got != "/case new" {
		t.Fatal(got)
	}
	RegisterCommandMentions(app, command, "another-id")
	SetCommandMentions(app, nil)
	if got := ResolveCommandMentions("/case new", app); got != "/case new" {
		t.Fatal(got)
	}
}

// TestCommandMentionsReachSendAndEdit resolves commands for channel sends
// and edits without changing the caller's message.
func TestCommandMentionsReachSendAndEdit(t *testing.T) {
	const app = "transport-mentions"
	SetCommandMentions(app, []*discordgo.ApplicationCommand{{ID: "123", Name: "help"}})
	original := Signal("info", "Try `/help`.", false)
	for _, content := range []string{
		original.ForApplication(app).Content,
		original.ForApplication(app).sendParams().Content,
		*EditMessage(original).ForApplication(app).Content,
		PrepareResponse(Public(original), app).Data.Content,
	} {
		if !strings.Contains(content, "</help:123>") {
			t.Fatal("transport did not resolve command", content)
		}
	}
	if !strings.Contains(original.Content, "`/help`") {
		t.Fatal("rendering mutated the caller's original")
	}
}
