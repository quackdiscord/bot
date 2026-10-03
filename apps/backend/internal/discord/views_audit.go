package discord

import (
	"fmt"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// auditMirrorMessage summarizes one audit entry for the staff mirror
// channel as a sentence with its details in subtext beneath it. Internal
// IDs stay in the audit log, not the copy.
func auditMirrorMessage(message quack.AuditMirrorMessage) Message {
	actor := "Quack"
	if message.ActorDiscordUserID != "" && message.ActorDiscordUserID != "quack-system" {
		actor = "<@" + message.ActorDiscordUserID + ">"
	}
	action := strings.NewReplacer("_", " ", ".", " ").Replace(message.Action)
	phrase := auditPhrases[message.Action]
	verb := phrase.verb
	if verb == "" {
		verb = "recorded **" + PlainText(action) + "**"
	}
	body := fmt.Sprintf("%s %s.", actor, verb)
	icon := phrase.icon
	if icon == "" {
		icon = "shield"
	}
	// Failure events such as case_action.failed already read as failures.
	if message.Result != quack.AuditResultSuccess && icon != "error" {
		body = fmt.Sprintf("%s couldn’t %s.", actor, PlainText(action))
		icon = "error"
	}
	// Every detail sits directly under the event in subtext; the general
	// conversation layout would leave a gap.
	details := []string{PlainText(message.FailureReason), RelativeTime(message.OccurredAt)}
	for _, detail := range details {
		for line := range strings.SplitSeq(detail, "\n") {
			if strings.TrimSpace(line) != "" {
				body += "\n-# " + line
			}
		}
	}
	return Signal(icon, body, false)
}

// auditPhrase is how the mirror words an audit action, and its icon.
type auditPhrase struct{ verb, icon string }

// auditPhrases covers the audit actions staff see most. Others fall back to
// "recorded <action>".
var auditPhrases = map[string]auditPhrase{
	"case.update":                  {"updated a case", "edit"},
	"case.create":                  {"added a case", "case_add"},
	"case.void":                    {"voided a case", "case_void"},
	"case.void.appeal":             {"voided a case after an appeal", "case_void"},
	"case_template.create":         {"created a template", "spark"},
	"case_template.update":         {"updated a template", "edit"},
	"case_template.archive":        {"archived a template", "lock"},
	"case_template.restore":        {"restored a template", "unlock"},
	"case_template.import":         {"imported templates", "case_add"},
	"case_template.export":         {"exported templates", "case"},
	"guild_settings.update":        {"updated Quack settings", "settings"},
	"case_action.attempt":          {"started an action attempt", "running"},
	"case_action.succeeded":        {"completed a Discord action", "success"},
	"case_action.failed":           {"recorded a failed Discord action", "error"},
	"case_action.skipped":          {"skipped a Discord action", "info"},
	"case_action.retrying":         {"scheduled another action attempt", "retry"},
	"case_action.retry":            {"queued another action attempt", "retry"},
	"case_action.dismiss":          {"dismissed an action failure", "review"},
	"case_action.reverse":          {"queued an action reversal", "retry"},
	"case_action.recovered":        {"recovered a stalled action", "retry"},
	"case_notification.sent":       {"sent the member a DM", "message"},
	"case_notification.failed":     {"couldn’t deliver the member’s DM", "error"},
	"appeal.submit":                {"submitted an appeal", "appeal"},
	"appeal.information.submit":    {"added information to an appeal", "reply"},
	"appeal.information_requested": {"asked for more information on an appeal", "reply"},
	"appeal.reopened":              {"reopened an appeal", "appeal"},
	"appeal.accepted":              {"accepted an appeal", "accept"},
	"appeal.rejected":              {"declined an appeal", "decline"},
	"appeal.close":                 {"closed an appeal", "lock"},
	"appeal.closed":                {"closed an appeal", "lock"},
	"appeal.settings.update":       {"updated the appeal settings", "settings"},
	"ticket.open":                  {"opened a ticket", "ticket"},
	"ticket.reply":                 {"replied to a ticket", "reply"},
	"ticket.resolve":               {"closed a ticket", "lock"},
	"ticket.cancel":                {"cancelled a ticket", "lock"},
	"honeypot.trigger":             {"recorded a honeypot trigger", "shield"},
	"v4_import.batch":              {"imported historical records", "history"},
}
