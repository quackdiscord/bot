package discord

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const helpCommandName = "help"

// helpCommand defines /help, which keeps explanations out of everyday
// results while making each workflow discoverable from Discord.
func helpCommand() *discordgo.ApplicationCommand {
	return &discordgo.ApplicationCommand{
		Name:        helpCommandName,
		Description: "Find your way around Quack",
		Options: []*discordgo.ApplicationCommandOption{{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "topic",
			Description: "What do you need help with?",
			Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Rules and repeat offenses", Value: "rules"},
				{Name: "Cases and evidence", Value: "cases"},
				{Name: "Appeals", Value: "appeals"},
				{Name: "Tickets and setup", Value: "setup"},
			},
		}},
	}
}

// helpTopics is the /help copy by topic; "" is the overview.
var helpTopics = map[string]string{
	"": "## Hey, I’m Quack.\nI keep track of moderation cases, handle appeals, and help your staff run the server.\n\n" +
		"**Moderation:** `/case add`, `/case list`, `/case user`\n" +
		"**Rules:** `/template create`, `/template view`\n" +
		"**Server setup:** `/setup tickets`, `/setup appeals`, `/setup honeypot`, `/setup logging`, `/setup audit`\n\n" +
		"Pick a topic in `/help` for a closer look.",
	"rules": "## Rules and repeat offenses\nA rule pairs a reason with a punishment. Pick the rule when adding a case; I’ll choose its punishment.\n\n" +
		"For example: **first time → warning, second time → 5-minute timeout, third time → ban.** Counts are per member, per rule.\n\n" +
		"Start with `/template create`. Add a step with `/template level`: `case:2 outcome:Timeout minutes:5`. The second case gets a timeout, and so do later cases until another step takes over.\n\n" +
		"`/template remove-level` removes a step by its count. Removing step 2 leaves the first step in effect until the next one.\n\n" +
		"Use `/template edit` to change the name, reason, appeals, or counting window. A seven-day window counts only cases from the last week. Older cases stay in history. Voided cases and imported v4 cases never raise the punishment. Changes apply to new cases.\n\n" +
		"`/template view` shows the current rule. Archive a rule to stop using it without deleting its history.",
	"cases": "## Cases and evidence\nUse `/case add`, or right-click a message or member and choose **Apps → Add case** (or **Add case for member**). Pick the rule and the case is created immediately.\n\n" +
		"Evidence is optional. Attach a file or message link when adding the case, or use `/case evidence` later. **Edit context** adds staff notes; **View evidence** opens saved copies. Those details stay private to staff. Check the copy result before deleting the source.\n\n" +
		"Made a mistake? **Void case** keeps the record but stops it counting against the member. It also queues removal of that case’s ban or timeout when possible. `/case view` shows the result.\n\n" +
		"Use `/case list` for recent cases, `/case user` for a member’s history, and `/case failures` when an action needs attention.",
	"appeals": "## Appeals\nMembers can appeal from their case DM, even after a ban. Each case gets one appeal.\n\n" +
		"Set the staff channel with `/setup appeals`. Review requests there, or find them with `/appeals`. Accepting voids the case and starts removing its ban or timeout. Rejecting keeps the case. The member gets the decision by DM.\n\n" +
		"Add a server invite with the `rejoin` option on `/setup appeals` so an unbanned member can return. Turn appeals on or off for a rule with `/template edit`.",
	"setup": "## Set up your server\nRun a setup command without channel options and I’ll create what it needs, or reuse its saved channels. You can choose existing channels instead.\n\n" +
		"`/setup tickets` creates a support panel and staff queue. Members open a private thread, chat with staff, then close it. The transcript is saved before the thread disappears and sent to the member by DM when possible.\n\n" +
		"`/setup honeypot` creates a trap channel with a warning. Check its rule before using it: messages there trigger the configured punishment. Moderators are exempt.\n\n" +
		"`/setup logging` records Discord events. `/setup audit` records Quack’s moderation activity.\n\n" +
		"For tickets, honeypot, or logging, use `enabled:false` to pause the feature and `enabled:true` to turn it back on. Use that option on its own; saved channels are kept.",
}

// helpPages is the staff dashboard page each /help topic links to; the
// overview links the guild's dashboard home.
var helpPages = map[string][]string{
	"":        nil,
	"rules":   {"rules"},
	"cases":   {"cases"},
	"appeals": {"appeals"},
	"setup":   {"settings"},
}

// help answers /help publicly with the chosen topic, or the overview. In a
// server, the answer links the topic's page in the dashboard.
func help(dashboard quack.DashboardLinks) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		topic := ""
		if option := i.ApplicationCommandData().GetOption("topic"); option != nil {
			topic = option.StringValue()
		}
		body, ok := helpTopics[topic]
		if !ok {
			topic, body = "", helpTopics[""]
		}
		return Immediate(Public(withLink(Content(body, false), dashboard.Staff(i.GuildID, helpPages[topic]...), dashboardLabel)))
	}
}
