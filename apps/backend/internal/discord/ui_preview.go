package discord

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
)

const uiPreviewCommandName = "ui-preview"

// previewSample is hand-written sample copy for the design gallery. Every
// record, conversation, and control in it is made up, including the DM
// examples.
type previewSample struct {
	name, group, icon, title, lead, detail, quote, meta string
	buttons                                             []string
}

// previewIconKeys is every icon the gallery shows and the upload check
// expects.
var previewIconKeys = strings.Fields("warn ban timeout kick unban untimeout shield case case_add case_void history note evidence search appeal review accept decline message reply ticket success error info pending running retry member join leave lock unlock delete edit link calendar pin settings spark duck")

// uiPreviewCommand defines /ui-preview, a development gallery of Quack's
// message designs. It is only registered in the dev environment.
func uiPreviewCommand() *discordgo.ApplicationCommand {
	permissions := int64(discordgo.PermissionManageGuild)
	dmAllowed := false
	return &discordgo.ApplicationCommand{
		Name:                     uiPreviewCommandName,
		Description:              "Compare new text messages with Quack's custom icons",
		DefaultMemberPermissions: &permissions,
		//lint:ignore SA1019 see moderatorCommand.
		DMPermission: &dmAllowed,
		Options: []*discordgo.ApplicationCommandOption{
			{Type: discordgo.ApplicationCommandOptionString, Name: "style", Description: "Pick a text design direction", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "All three text designs", Value: "all"}, {Name: "A · Compact", Value: "compact"}, {Name: "B · Conversation", Value: "conversation"}, {Name: "C · Spotlight", Value: "spotlight"},
			}},
			{Type: discordgo.ApplicationCommandOptionString, Name: "section", Description: "Compare a smaller group, or browse the icon set", Choices: []*discordgo.ApplicationCommandOptionChoice{
				{Name: "Every message", Value: "all"}, {Name: "Cases and moderation", Value: "cases"}, {Name: "Punishment DMs", Value: "dms"}, {Name: "Appeals", Value: "appeals"}, {Name: "Tickets", Value: "tickets"}, {Name: "Activity and errors", Value: "activity"}, {Name: "All 40 icons", Value: "icons"},
			}},
		},
	}
}

// uiPreview posts the gallery in the invoking channel with the invoking
// application's own uploaded emoji, read live so a missing upload shows up
// before anything is posted. It never sends DMs or touches records.
func uiPreview(bot *Bot) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if i.Member == nil || i.Member.User == nil || i.Member.Permissions&(discordgo.PermissionManageGuild|discordgo.PermissionAdministrator) == 0 {
			return Immediate(Error("You need Manage Server to preview designs."))
		}
		data := i.ApplicationCommandData()
		styles := []string{"conversation"}
		if option := data.GetOption("style"); option != nil {
			switch selected := option.StringValue(); selected {
			case "all":
				styles = []string{"compact", "conversation", "spotlight"}
			case "compact", "conversation", "spotlight":
				styles = []string{selected}
			default:
				return Immediate(Error("Choose Compact, Conversation, or Spotlight."))
			}
		}
		section := "all"
		if option := data.GetOption("section"); option != nil {
			section = option.StringValue()
		}
		if !slices.Contains([]string{"all", "cases", "dms", "appeals", "tickets", "activity", "icons"}, section) {
			return Immediate(Error("Choose a section from the preview menu."))
		}
		return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
			// Application emoji belong to their bot, so another bot's IDs
			// would not render.
			catalog, err := bot.Session.ApplicationEmojis(i.AppID, rest(ctx)...)
			if err != nil {
				_, err = responder.EditOriginal(EditMessage(Content("I couldn’t load this bot’s custom icons. Try the preview again in a moment.", true)))
				return err
			}
			icons, missing := previewIconCatalog(catalog)
			if len(missing) != 0 {
				_, err = responder.EditOriginal(EditMessage(Content("This bot still needs these icons uploaded: `"+strings.Join(missing, "`, `")+"`.", true)))
				return err
			}
			send := func(message Message) error {
				_, err := bot.Send(ctx, i.ChannelID, message)
				return err
			}
			if section == "icons" {
				if err := send(previewIconGallery(icons)); err != nil {
					return err
				}
			} else {
				var samples []previewSample
				for _, sample := range uiPreviewSamples(i.Member.User, time.Now()) {
					if section == "all" || sample.group == section {
						samples = append(samples, sample)
					}
				}
				for _, style := range styles {
					intro := "## " + icons["duck"] + " " + previewStyleName(style) + "\n" + previewStyleDescription(style) + "\n-# Design samples only · Buttons are disabled · DM examples appear here."
					if err := send(Content(intro, false)); err != nil {
						return err
					}
					for n, sample := range samples {
						if err := ctx.Err(); err != nil {
							return err
						}
						message := previewDesign(sample, style, icons)
						message.Content += fmt.Sprintf("\n-# %s · %02d/%02d · %s", previewStyleName(style), n+1, len(samples), sample.name)
						if len([]rune(message.Content)) > contentLimit {
							return fmt.Errorf("preview %s exceeds Discord's content limit", sample.name)
						}
						if err := send(message); err != nil {
							return fmt.Errorf("preview %s / %s: %w", style, sample.name, err)
						}
					}
				}
			}
			_, err = responder.EditOriginal(EditMessage(Content("Your previews are ready. Use `/ui-preview style: section:` to compare a smaller group.", true)))
			return err
		})
	}
}

// previewIconCatalog resolves names from the authenticated bot's own catalog.
func previewIconCatalog(catalog []*discordgo.Emoji) (map[string]string, []string) {
	icons := make(map[string]string)
	for _, emoji := range catalog {
		if emoji != nil && emoji.ID != "" && strings.HasPrefix(emoji.Name, "quack_") {
			icons[strings.TrimPrefix(emoji.Name, "quack_")] = emoji.MessageFormat()
		}
	}
	var missing []string
	for _, key := range previewIconKeys {
		if icons[key] == "" {
			missing = append(missing, "quack_"+key)
		}
	}
	return icons, missing
}

// previewStyleName gives each draft a stable name for design feedback.
func previewStyleName(style string) string {
	switch style {
	case "conversation":
		return "B · Conversation"
	case "spotlight":
		return "C · Spotlight"
	default:
		return "A · Compact"
	}
}

// previewStyleDescription explains the intended density before a gallery begins.
func previewStyleDescription(style string) string {
	switch style {
	case "conversation":
		return "Natural sentences, a little breathing room, and quoted context."
	case "spotlight":
		return "A short headline for the outcome, followed by the human details."
	default:
		return "The outcome first. One supporting line, with references kept small."
	}
}

// previewDesign renders hand-written copy into three text hierarchies. Context
// quotes are never rendered as field lists, and every control stays inert.
func previewDesign(sample previewSample, style string, icons map[string]string) Message {
	icon := icons[sample.icon]
	var parts []string
	switch style {
	case "conversation":
		parts = append(parts, icon+" "+sample.lead)
		if sample.quote != "" {
			parts = append(parts, "> "+strings.ReplaceAll(sample.quote, "\n", "\n> "))
		}
		if sample.detail != "" {
			parts = append(parts, sample.detail)
		}
	case "spotlight":
		parts = append(parts, "### "+icon+" "+sample.title, sample.lead)
		if sample.detail != "" {
			parts = append(parts, sample.detail)
		}
		if sample.quote != "" {
			parts = append(parts, "> "+strings.ReplaceAll(sample.quote, "\n", "\n> "))
		}
	default:
		lead := icon + " " + sample.lead
		if sample.detail != "" {
			lead += "\n" + sample.detail
		}
		parts = append(parts, lead)
		if sample.quote != "" {
			parts = append(parts, "-# "+strings.ReplaceAll(sample.quote, "\n", "\n-# "))
		}
	}
	content := strings.Join(parts, "\n\n")
	if sample.meta != "" {
		content += "\n-# " + sample.meta
	}
	if style == "conversation" {
		content = strings.ReplaceAll(discordtext.Conversation(sample.icon, sample.lead, sample.quote, sample.detail, sample.meta), discordtext.Icon(sample.icon), icon)
	}
	message := Message{Content: content, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	if len(sample.buttons) != 0 {
		var buttons []discordgo.MessageComponent
		for i, label := range sample.buttons {
			buttons = append(buttons, Button(fmt.Sprintf("preview:disabled:v1:%d", i), label, discordgo.SecondaryButton, true))
		}
		message.Components = []discordgo.MessageComponent{Row(buttons...)}
	}
	return message
}

// previewIconGallery allows small-size inspection of every uploaded application icon.
func previewIconGallery(icons map[string]string) Message {
	lines := []string{"### " + icons["duck"] + " Quack icons"}
	for _, key := range previewIconKeys {
		lines = append(lines, icons[key]+" `"+key+"`")
	}
	return Message{Content: strings.Join(lines, "\n"), AllowedMentions: &discordgo.MessageAllowedMentions{}}
}

// uiPreviewSamples covers the current command, notification, and log surfaces
// using purpose-written prose. All identities and timestamps are presentation
// samples; no domain services, persistence, or actual DM delivery are involved.
func uiPreviewSamples(user *discordgo.User, now time.Time) []previewSample {
	member := "<@" + user.ID + ">"
	ago := RelativeTime(now.Add(-8 * time.Minute))
	until := RelativeTime(now.Add(time.Hour))
	exact := fmt.Sprintf("<t:%d:f>", now.Add(time.Hour).Unix())
	ref := "Case #12 · Second warning · " + ago
	reason := "Repeated the same link after being asked to stop."
	var samples []previewSample
	add := func(name, group, icon, title, lead, detail, quote, meta string, buttons ...string) {
		samples = append(samples, previewSample{name: name, group: group, icon: icon, title: title, lead: lead, detail: detail, quote: quote, meta: meta, buttons: buttons})
	}
	caseLead := "Case added for " + member + " for **Repeated spam**."
	add("Case added · applied", "cases", "case_add", "Case added", caseLead, "Timed out for **1 hour**. They can chat again "+until+".", "", ref, "View case")
	add("Case added · queued", "cases", "pending", "Recorded. Timeout queued.", caseLead, "The timeout is queued. Its result will appear on the case.", "", ref, "View case")
	add("Case added · failed", "cases", "error", "Recorded. Timeout failed.", caseLead, "I couldn’t apply the timeout. Check **Moderate Members** and my role position, then retry.", "", ref, "View case", "Retry timeout")
	add("Case added · record only", "cases", "note", "A note on the record", caseLead, "This is a record only; no punishment was applied.", "", ref, "View case")
	add("Case detail", "cases", "case", "Repeated spam", member+" received a **1-hour timeout** for **Repeated spam**.", "The timeout ends "+exact+". The notification was delivered.\n[Open the evidence](https://discord.com/channels/1005778938108325970/1005778939068813442)", reason, ref, "History", "Void case")
	add("Voided case", "cases", "case_void", "This case was voided", "**Case #12** for "+member+" was voided.", "The original decision stays in the history. Quack will try to remove its ban or timeout; removal failures stay available for review.", "Added to the wrong member.", "Originally Repeated spam · "+ago, "View history")
	add("Recent cases", "cases", "case", "Recent decisions", "**3 recent cases** in Quack’s Pond.", "`#12`  "+member+" · **Repeated spam** · 1-hour timeout · "+ago+"\n`#11`  "+member+" · **Off-topic** · warning · "+RelativeTime(now.Add(-24*time.Hour))+"\n`#10`  ~~Repeated spam~~ · voided · "+RelativeTime(now.Add(-48*time.Hour)), "", "Showing 3 of 13 · Page 1", "Previous", "Next")
	add("Member history", "cases", "history", "A little context", member+" has **2 active cases** and **1 voided case**.", "`#12`  **Repeated spam** → 1-hour timeout · "+ago+"\n`#11`  **Off-topic** → warning · "+RelativeTime(now.Add(-24*time.Hour))+"\n`#10`  ~~Repeated spam~~ → voided", "", "Newest first · Voided records remain visible", "View case")
	add("No cases", "cases", "search", "Nothing on record", "No cases for "+member+" yet.", "Their history will appear here when a case is added.", "", "")
	add("Action failure queue", "cases", "error", "One action needs attention", "The timeout for "+member+" on **case #12** didn’t go through.", "Discord reported missing permissions. Check my role and **Moderate Members**, then retry.", "", "1 unresolved action · "+ago, "Retry", "Dismiss")
	add("No action failures", "cases", "success", "All clear", "No failed actions are waiting for review.", "", "", "")
	add("Retry queued", "cases", "retry", "Trying again", "The timeout for **case #12** is queued for another attempt.", "The case will show whether it succeeds.", "", "", "View case")
	add("Failure dismissed", "cases", "success", "Dismissed", "This failure is no longer in the review queue.", "The attempt and its error remain on **case #12**.", "", "", "View case")
	add("Case voided", "cases", "case_void", "Correction saved", "Voided **case #12** for "+member+".", "The original decision and this correction are both in the history.", "Added to the wrong member.", "", "View history")
	add("Reversal queued", "cases", "untimeout", "Removal queued", "I’ve queued the removal of "+member+"’s timeout.", "The original action remains in the history. The timeout is still active until the removal succeeds.", "", "Case #12", "View case")
	add("Confirmation", "cases", "warn", "Void this case?", "Void **case #12** for "+member+"?", "This marks the decision as void and keeps its history. It doesn’t remove the timeout.", "Repeated spam · Second warning", "", "Keep case", "Void case")
	add("Context saved", "cases", "note", "Context saved", "Your note is ready. Finish adding the case when you’re ready.", "", reason, "No case has been created yet", "Continue")
	add("Template picker", "cases", "shield", "What happened?", "Choose the rule that best fits this message.", "**Repeated spam** — the same content posted repeatedly.\n**Off-topic** — a conversation that belongs elsewhere.", "", "You can review the action before adding the case.", "Repeated spam", "Off-topic")
	add("Error · missing access", "activity", "lock", "You need a moderator role", "Only moderators can add cases.", "Ask a server admin to give you the moderator role, then try again.", "", "Nothing was changed.")
	add("Error · Discord unavailable", "activity", "error", "Discord didn’t respond", "I couldn’t confirm that request with Discord.", "Check the case before trying again so you don’t submit it twice.", "", "")
	add("Error · missing case", "activity", "search", "Case not found", "I couldn’t find **case #12** in this server.", "Check the case number and try again.", "", "")
	add("Staff appeal review", "appeals", "review", "A decision to revisit", member+" appealed **case #12 · Repeated spam**.", "The original action was a **1-hour timeout**. Review the context before deciding.", "I understand the rule. Could you review the timeout?", "Submitted "+ago, "View case", "Accept", "Decline")
	add("Appeal entry", "appeals", "appeal", "Want a second look?", "If you think this decision was a mistake, you can ask the team to review it.", "Explain what happened and what you’d like staff to reconsider.", "", "Case #12 · Quack’s Pond", "Appeal decision")
	for _, dm := range []struct{ name, icon, title, lead, detail string }{
		{"Warning", "warn", "A warning from Quack’s Pond", "You received a warning in **Quack’s Pond** for **Repeated spam**.", "Please give others room to talk and avoid repeating messages."},
		{"Timeout", "timeout", "A pause from chat", "You’ve been timed out in **Quack’s Pond** for **Repeated spam**.", "You can chat again " + until + " — " + exact + "."},
		{"Kick", "kick", "Removed from Quack’s Pond", "You’ve been removed from **Quack’s Pond** for **Repeated spam**.", "You can ask staff to review the decision below."},
		{"Ban", "ban", "Banned from Quack’s Pond", "You’ve been banned from **Quack’s Pond** for **Repeated spam**.", "If you think this was a mistake, you can appeal below."},
		{"Timeout removed", "untimeout", "You can chat again", "Your timeout in **Quack’s Pond** has been removed.", "You can send messages again. Please keep the server rules in mind."},
		{"Ban removed", "unban", "Your ban was lifted", "Your ban from **Quack’s Pond** has been removed.", "You’ll need a valid server invite if you’d like to rejoin."},
		{"Action failed", "error", "A case was recorded", "A case was added for you in **Quack’s Pond** for **Repeated spam**.", "The timeout couldn’t be applied. Staff will review the case."},
	} {
		quote := reason
		var buttons []string
		if dm.name == "Warning" || dm.name == "Timeout" || dm.name == "Kick" || dm.name == "Ban" || dm.name == "Action failed" {
			buttons = []string{"Appeal decision"}
		} else {
			quote = ""
		}
		add("Punishment DM · "+dm.name, "dms", dm.icon, dm.title, dm.lead, dm.detail, quote, "Case #12 · "+ago, buttons...)
	}
	add("Custom DM", "dms", "message", "A note from the team", "The **Quack’s Pond** team sent you a note.", "Contact the server’s staff if you have a question.", "Please take a moment to review the server rules.", "")
	add("Appeal DM · received", "appeals", "appeal", "Your appeal is with the team", "Staff received your appeal for **case #12** in **Quack’s Pond**.", "We’ll let you know here when they decide.", "", "Submitted "+ago)
	add("Appeal DM · accepted", "appeals", "accept", "Your appeal was accepted", "The **Quack’s Pond** team accepted your appeal for **case #12**.", "Staff will review any punishment that needs to be removed. Check your case for removal updates.", "Thanks for explaining what happened.", ago)
	add("Appeal DM · declined", "appeals", "decline", "Your appeal was declined", "The **Quack’s Pond** team reviewed your appeal for **case #12** and kept the original decision.", "The case remains on your record.", "The messages continued after the first warning.", ago)
	add("Appeal staff alert", "appeals", "review", "An appeal is waiting", member+" asked for another look at **case #12 · Repeated spam**.", "", "I understand the rule. Could you review the timeout?", ago, "Review appeal")
	add("Audit · success", "activity", "case_add", "A case was added", member+" added **case #12** for **Repeated spam**.", "", "", ago, "View case")
	add("Audit · denied", "activity", "lock", "Quack was missing a permission", member+" tried to add a case, but Quack lacks **Ban Members**.", "No case was created.", "", ago)
	add("Ticket entry", "tickets", "ticket", "Need a hand?", "Open a private ticket and tell us what’s going on.", "Only you and the staff team can see the conversation.", "", "Quack’s Pond support", "Open ticket")
	add("Ticket opened", "tickets", "ticket", "You’re in the queue", "Your private ticket is ready. Tell us what you need help with.", "A staff member will reply here.", "", "Ticket #12", "Close ticket")
	add("Ticket queue", "tickets", "pending", "Two people are waiting", "**2 tickets** are waiting for a staff reply.", "`#12`  "+member+" · Understanding a rule · opened "+RelativeTime(now.Add(-20*time.Minute))+"\n`#11`  "+member+" · Account question · opened "+RelativeTime(now.Add(-time.Hour)), "", "Oldest waiting: 1 hour", "Open queue")
	add("Ticket detail", "tickets", "message", "Understanding a rule", "A conversation with "+member+" in **ticket #12**.", "**Staff:** Happy to help. Which rule?", "I need help understanding a server rule.", "Last reply "+ago, "Reply", "Close ticket")
	add("Ticket reply", "tickets", "reply", "Reply sent", "Your reply was sent to "+member+" in **ticket #12**.", "", "Happy to help. Which rule?", "")
	add("Ticket closed", "tickets", "lock", "Conversation closed", "Closed **ticket #12** with "+member+".", "The transcript has been saved.", "", ago, "View transcript")
	add("Ticket permissions repaired", "tickets", "unlock", "Access restored", member+" and the staff team can access **ticket #12** again.", "The ticket’s permissions have been repaired.", "", "", "Open ticket")
	add("General staff log", "activity", "delete", "A message was deleted", "A message from "+member+" was deleted in <#1005778939068813442>.", "", "Please review the server rules.", ago)
	add("Member joined", "activity", "join", "A new arrival", member+" joined **Quack’s Pond**.", "", "", RelativeTime(now))
	add("Member left", "activity", "leave", "A member left", member+" left **Quack’s Pond**.", "", "", RelativeTime(now))
	add("Message edited", "activity", "edit", "A message changed", member+" edited a message in <#1005778939068813442>.", "~~Read the rules.~~ → Please take a moment to read the rules.", "", ago)
	return samples
}
