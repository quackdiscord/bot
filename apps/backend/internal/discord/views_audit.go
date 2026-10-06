package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// auditMirrorMessage summarizes one audit entry for the staff mirror
// channel in one line, with the case it concerns and when it happened in
// subtext directly beneath it. Internal IDs stay in the audit log and
// controls, not the copy. Entries with a dashboard page link to it inline,
// the way the dashboard's audit log does: the phrase's object ("an appeal")
// becomes a bold link, or else "Case #N" does, or else a link line is added
// (see auditMirrorLink). A failed action staff can still retry gets a
// "Retry action" button, which rechecks permissions like /case retry.
func auditMirrorMessage(message quack.AuditMirrorMessage, dashboard quack.DashboardLinks) Message {
	actor := "Quack"
	if message.ActorDiscordUserID != "" && message.ActorDiscordUserID != "quack-system" {
		actor = "<@" + message.ActorDiscordUserID + ">"
	}
	action := strings.NewReplacer("_", " ", ".", " ").Replace(message.Action)
	phrase := auditPhrases[message.Action]
	url, label := auditMirrorLink(message, dashboard)
	verb, linked := linkObject(phrase.verb, url)
	if verb == "" {
		verb = "recorded **" + PlainText(action) + "**"
	}
	succeeded := message.Result == quack.AuditResultSuccess
	body := fmt.Sprintf("%s %s.", actor, verb)
	if !succeeded {
		body, linked = fmt.Sprintf("%s couldn’t %s.", actor, PlainText(action)), false
	}
	if message.ActionType != "" {
		kind := PlainText(message.ActionType.Label())
		switch message.Action {
		case "case_action.succeeded":
			body, linked = kind+" action completed.", false
		case "case_action.failed":
			body, linked = kind+" action failed.", false
		case "case_action.skipped":
			body, linked = kind+" action skipped.", false
		}
	}
	if message.ReversalNoop && message.Action == "case_action.succeeded" {
		body, linked = "The punishment had already ended.", false
	}
	context := ""
	if message.CaseID != "" {
		context = fmt.Sprintf("Case #%d", message.CaseNumber)
		if !linked && url != "" && message.ResourceType != "appeal" {
			context, linked = boldLink(context, url), true
		}
		if message.TargetDiscordUserID != "" {
			context += " · <@" + message.TargetDiscordUserID + ">"
		}
		if message.RuleName != "" {
			context += " · " + PlainText(message.RuleName)
		}
	}
	if message.Action == string(quack.AuditActionCaseCreate) && message.SelectedOutcome != "" {
		if message.SelectedLevelName != "" {
			context += "\nLevel: " + PlainText(message.SelectedLevelName)
		}
		context += "\nOutcome: " + PlainText(message.SelectedOutcome)
	}
	icon := phrase.icon
	if icon == "" {
		icon = "shield"
	}
	if !succeeded {
		icon = "error"
	}
	// Every detail sits directly under the event in subtext; the general
	// conversation layout would leave a gap.
	details := []string{context, PlainText(message.FailureReason)}
	if !linked && url != "" {
		details = append(details, boldLink(label, url))
	}
	if strings.HasPrefix(message.Action, "case_action.") {
		details = append(details, "By "+actor)
	}
	details = append(details, RelativeTime(message.OccurredAt))
	for _, detail := range details {
		for line := range strings.SplitSeq(detail, "\n") {
			if strings.TrimSpace(line) != "" {
				body += "\n-# " + line
			}
		}
	}
	notice := Signal(icon, body, false)
	if message.RetryExecutionID != "" && message.Result == quack.AuditResultFailure {
		if id, err := EncodeCustomID(CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: message.RetryExecutionID}); err == nil {
			notice.Components = []discordgo.MessageComponent{Row(Button(id, "Retry action", discordgo.SecondaryButton, false))}
		}
	}
	return notice
}

// linkObject fills an audit phrase's {object} in: a bold link to url, or
// plain text when there is no url. It reports whether it added the link.
func linkObject(phrase, url string) (string, bool) {
	start := strings.IndexByte(phrase, '{')
	end := strings.IndexByte(phrase, '}')
	if start < 0 || end < start {
		return phrase, false
	}
	object := phrase[start+1 : end]
	if url == "" {
		return phrase[:start] + object + phrase[end+1:], false
	}
	return phrase[:start] + boldLink(object, url) + phrase[end+1:], true
}

// boldLink is a bold Markdown link to a dashboard url. The angle brackets
// keep Discord from previewing it.
func boldLink(text, url string) string {
	return "**[" + text + "](<" + url + ">)**"
}

// auditMirrorLink picks the staff dashboard page an audit entry is about:
// the appeal, the case, the rule, Quack's settings, or a module's settings.
// Entries without a page, such as guild lifecycle and imports, get "".
func auditMirrorLink(message quack.AuditMirrorMessage, dashboard quack.DashboardLinks) (string, string) {
	guildID := message.DiscordGuildID
	area, _, _ := strings.Cut(message.Action, ".")
	switch {
	case message.ResourceType == "appeal" && message.ResourceID != "":
		return dashboard.Staff(guildID, "appeals", message.ResourceID), dashboardAppealLabel
	case message.ResourceType == "appeal":
		return dashboard.Staff(guildID, "appeals"), dashboardLabel
	case message.CaseID != "":
		return dashboard.Staff(guildID, "cases", message.CaseID), dashboardCaseLabel
	case message.ResourceType == "case_template" && message.ResourceID != "":
		return dashboard.Staff(guildID, "rules", message.ResourceID), dashboardRuleLabel
	case area == "case_template":
		return dashboard.Staff(guildID, "rules"), dashboardLabel
	case area == "guild_settings":
		return dashboard.Staff(guildID, "settings"), dashboardSetupLabel
	case area == "ticket":
		return dashboard.Staff(guildID, "modules", "tickets"), dashboardLabel
	case area == "honeypot":
		return dashboard.Staff(guildID, "modules", "honeypot"), dashboardSetupLabel
	}
	return "", ""
}

// auditPhrase is how the mirror words an audit action, and its icon. The
// verb's {object} links to the entry's dashboard page.
type auditPhrase struct{ verb, icon string }

// auditPhrases covers the audit actions staff see most. Others fall back to
// "recorded <action>".
var auditPhrases = map[string]auditPhrase{
	"case.update":                  {"updated {a case}", "edit"},
	"case.create":                  {"added {a case}", "case_add"},
	"case.void":                    {"voided {a case}", "case_void"},
	"case.void.appeal":             {"voided {a case} after an appeal", "case_void"},
	"case_template.create":         {"created {a template}", "spark"},
	"case_template.update":         {"updated {a template}", "edit"},
	"case_template.archive":        {"archived {a template}", "lock"},
	"case_template.restore":        {"restored {a template}", "unlock"},
	"case_template.import":         {"imported {templates}", "case_add"},
	"case_template.export":         {"exported {templates}", "case"},
	"guild_settings.update":        {"updated {Quack settings}", "settings"},
	"case_action.attempt":          {"started an action attempt on {a case}", "running"},
	"case_action.succeeded":        {"completed a Discord action on {a case}", "success"},
	"case_action.failed":           {"recorded a failed Discord action on {a case}", "error"},
	"case_action.skipped":          {"skipped a Discord action on {a case}", "info"},
	"case_action.retrying":         {"scheduled another action attempt on {a case}", "retry"},
	"case_action.retry":            {"queued another action attempt on {a case}", "retry"},
	"case_action.dismiss":          {"dismissed an action failure on {a case}", "review"},
	"case_action.reverse":          {"queued a reversal on {a case}", "retry"},
	"case_action.recovered":        {"recovered a stalled action on {a case}", "retry"},
	"case_notification.sent":       {"sent the member a DM about {a case}", "message"},
	"case_notification.failed":     {"couldn’t deliver the member’s DM about {a case}", "error"},
	"appeal.submit":                {"submitted {an appeal}", "appeal"},
	"appeal.information.submit":    {"added information to {an appeal}", "reply"},
	"appeal.information_requested": {"asked for more information on {an appeal}", "reply"},
	"appeal.reopened":              {"reopened {an appeal}", "appeal"},
	"appeal.accepted":              {"accepted {an appeal}", "accept"},
	"appeal.rejected":              {"declined {an appeal}", "decline"},
	"appeal.close":                 {"closed {an appeal}", "lock"},
	"appeal.closed":                {"closed {an appeal}", "lock"},
	"ticket.open":                  {"opened {a ticket}", "ticket"},
	"ticket.reply":                 {"replied to {a ticket}", "reply"},
	"ticket.resolve":               {"closed {a ticket}", "lock"},
	"ticket.cancel":                {"cancelled {a ticket}", "lock"},
	"honeypot.trigger":             {"recorded {a honeypot trigger}", "shield"},
	"v4_import.batch":              {"imported historical records", "history"},
}
