package discord

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// pageBudget is how much text one page of a paged staff view holds, leaving
// room under Discord's limit for its footer and command mentions.
const pageBudget = 1750

// caseReceiptMessage announces a case in the channel it was created from:
// who, which rule, what Quack did about it, and the context staff gave,
// never evidence. Member notifications have their own wording. A receipt
// too long for one message shows its first page and a "View full case"
// button. caseURL, when set, is the case's staff dashboard page, linked
// beside the case controls.
func caseReceiptMessage(receipt *quack.CaseReceipt, caseURL string) Message {
	if receipt == nil {
		return Signal("case_add", "Case added.", false)
	}
	meta := []string{fmt.Sprintf("Case #%d", receipt.CaseNumber)}
	if date := RelativeTime(receipt.CreatedAt); date != "" {
		meta = append(meta, date)
	}
	icon := "case_add"
	if len(receipt.Actions) == 0 {
		icon = "warn"
	}
	for _, action := range receipt.Actions {
		if action.Status == quack.ActionExecutionFailed {
			icon = "error"
			break
		}
		if pendingStatus(action.Status) {
			icon = "pending"
		}
	}
	status := publicActionStatus(receipt.Actions)
	if receipt.ModeratorDiscordUserID != "" {
		status += "\nModerator: <@" + receipt.ModeratorDiscordUserID + ">"
	}
	if context := contextSummary(receipt.ContextValues); context != "" {
		status += "\n\n" + context
	}
	if receipt.EvidenceIncomplete {
		status += "\nSome evidence couldn’t be saved. Staff can check **View evidence**."
	}
	lead := fmt.Sprintf("Case #%d · <@%s>", receipt.CaseNumber, receipt.TargetDiscordUserID)
	if name := strings.TrimSpace(receipt.RuleName); name != "" {
		lead += " · **" + PlainText(name) + "**"
	}
	voided := receipt.Validity == quack.CaseValidityVoided
	if voided {
		lead = "**Voided** · " + lead
	}
	message := Conversation(icon, lead, PlainText(receipt.Reason), status, strings.Join(meta, " · "), false)
	controls := appendLink(casePrimaryControls(receipt.CaseID, receipt.TargetDiscordUserID, voided), caseURL, dashboardLabel)
	message.Components = []discordgo.MessageComponent{Row(controls...)}
	if pages := TextPages(message.Content, pageBudget); len(pages) > 1 {
		message.Content = pages[0]
		message.Components = append(message.Components, Row(caseButton("view", receipt.CaseID, "View full case", discordgo.SecondaryButton, false)))
	}
	return message
}

// pendingStatus reports whether an execution has not settled yet.
func pendingStatus(status quack.ActionExecutionStatus) bool {
	return status == quack.ActionExecutionPending || status == quack.ActionExecutionRunning || status == quack.ActionExecutionRetrying
}

// publicActionStatus describes each action's progress, never calling a
// queued action done.
func publicActionStatus(actions []quack.CaseReceiptAction) string {
	if len(actions) == 0 {
		return "Warning recorded."
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, actionSentence(action.ActionType, action.Status, action.TimeoutUntil))
	}
	return strings.Join(parts, "\n")
}

// casePrimaryControls are the controls every case receipt and detail
// carries: Edit context, View evidence, History, and Void case.
func casePrimaryControls(caseID, targetID string, voided bool) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		caseButton("edit_context", caseID, "Edit context", discordgo.SecondaryButton, false),
		caseButton("evidence", caseID, "View evidence", discordgo.SecondaryButton, false),
		caseButton("user_detail", targetID, "History", discordgo.SecondaryButton, false),
		caseButton("void", caseID, "Void case", discordgo.DangerButton, voided),
	}
}

// caseButton returns a button routed to the case namespace.
func caseButton(action, payload, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	return Button(MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: payload}), label, style, disabled)
}

// caseVoidedMessage confirms a void, and says when a punishment is still
// being applied or removed.
func caseVoidedMessage(item *quack.CaseResponse) Message {
	status := "It stays in history and no longer counts toward escalation."
	for _, action := range item.Actions {
		if action.ActionType == quack.ActionRemoveTimeout || action.ActionType == quack.ActionUnbanUser {
			status += "\n" + ActionSentence(action.ActionType, action.Status)
		} else if action.Status == quack.ActionExecutionRunning {
			status += "\nEnforcement is still finishing. Quack will try to undo any ban or timeout that succeeds."
		}
	}
	return Conversation("case_void", fmt.Sprintf("Case #%d was voided.", item.CaseNumber), "", status, "", false)
}

// caseDetailPage is page (1-based, clamped) of the staff view of a case.
// Long context and history page in Discord rather than spilling into a
// file, and every page keeps the case controls. Icons are resolved for
// applicationID before measuring, because they count toward Discord's
// limit.
func caseDetailPage(detail *quack.CaseDetailResponse, page int, applicationID string) Message {
	message := caseDetailMessage(detail)
	if detail == nil {
		return message
	}
	pages := TextPages(discordtext.Resolve(message.Content, applicationID), pageBudget)
	page = min(max(page, 1), len(pages))
	message.Content = pages[page-1]
	if len(pages) > 1 {
		message.Content += fmt.Sprintf("\n\n-# Case #%d · Page %d/%d", detail.CaseNumber, page, len(pages))
		controls, _ := Pagination("case", "detail", fmt.Sprintf("%d|%s", page, detail.ID), page, len(pages))
		message.Components = append(message.Components, controls...)
	}
	return message
}

// caseDetailMessage is the staff view of one case, written as a
// conversation: the decision and reason, then outcome, context, evidence,
// and recent history, with correction and recovery controls.
func caseDetailMessage(detail *quack.CaseDetailResponse) Message {
	if detail == nil {
		return Signal("error", "That case couldn’t be found. Check its number and try again.", true)
	}
	lead := fmt.Sprintf("Case #%d · <@%s>", detail.CaseNumber, detail.TargetDiscordUserID)
	if detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Name != "" {
		lead += " · **" + PlainText(detail.TemplateSnapshot.Template.Name) + "**"
	}
	icon := "case"
	var parts []string
	if detail.ModeratorDiscordUserID != "" && detail.Source != quack.CaseSourceV4Import {
		parts = append(parts, "Moderator: <@"+detail.ModeratorDiscordUserID+">")
	}
	if detail.Validity == quack.CaseValidityVoided {
		icon = "case_void"
		lead = "**Voided** · " + lead
		parts = append(parts, "This case was voided and no longer counts toward escalation.")
		if detail.VoidedReason != "" {
			parts = append(parts, Quote(PlainText(detail.VoidedReason)))
		}
	}
	outcome := []string{staffActionSummary(detail.Actions)}
	if detail.Source == quack.CaseSourceV4Import {
		outcome = []string{"Imported v4 history: " + historicalCaseLabel(detail.CaseResponse) + ". No new action was performed."}
		if detail.ModeratorDiscordUserID != "" {
			parts = append(parts, "Original moderator: <@"+detail.ModeratorDiscordUserID+">")
		}
		if link := historicalContextLink(detail.ContextURL); link != "" {
			parts = append(parts, link)
		}
	}
	if detail.Notification != nil {
		outcome = append(outcome, notificationDeliverySentence(string(detail.Notification.Status)))
	}
	if detail.Validity != quack.CaseValidityVoided && detail.TemplateSnapshot != nil && detail.TemplateSnapshot.Template.Appealable {
		outcome = append(outcome, "The member can appeal this case.")
	}
	parts = append(parts, strings.Join(outcome, " "))
	for _, context := range []string{contextSummary(detail.ContextValues), evidenceSummary(detail.Evidence), eventSummary(detail.Events)} {
		if context != "" {
			parts = append(parts, context)
		}
	}
	meta := []string{fmt.Sprintf("Case #%d", detail.CaseNumber)}
	if detail.SelectedLevel != nil {
		meta = append(meta, PlainText(detail.SelectedLevel.Name))
	}
	if date := RelativeTime(detail.CreatedAt); date != "" {
		meta = append(meta, "Created "+date)
	}
	message := Conversation(icon, lead+".", PlainText(detail.Reason), strings.Join(parts, "\n\n"), strings.Join(meta, " · "), false)
	message.Components = caseDetailControls(detail)
	return message
}

// caseDetailControls adds a recovery row under the primary controls: Retry
// and Dismiss for the first failed action, or Reverse for the first
// succeeded timeout or ban.
func caseDetailControls(detail *quack.CaseDetailResponse) []discordgo.MessageComponent {
	rows := []discordgo.MessageComponent{Row(casePrimaryControls(detail.ID, detail.TargetDiscordUserID, detail.Validity == quack.CaseValidityVoided)...)}
	var buttons []discordgo.MessageComponent
	for _, action := range detail.Actions {
		if action.Status == quack.ActionExecutionFailed {
			buttons = append(buttons,
				caseButton("retry", action.ID, "Retry", discordgo.SecondaryButton, false),
				caseButton("dismiss", action.ID, "Dismiss", discordgo.SecondaryButton, false),
			)
			break
		}
		if action.Status != quack.ActionExecutionSucceeded {
			continue
		}
		var reversal quack.ActionType
		switch action.ActionType {
		case quack.ActionTimeoutUser:
			reversal = quack.ActionRemoveTimeout
		case quack.ActionBanUser:
			reversal = quack.ActionUnbanUser
		default:
			continue
		}
		payload := strings.Join([]string{detail.ID, action.ID, string(reversal)}, "|")
		buttons = append(buttons, caseButton("reverse", payload, "Reverse action", discordgo.SecondaryButton, false))
		break
	}
	if len(buttons) > 0 {
		rows = append(rows, Row(buttons...))
	}
	return rows
}

// notificationDeliverySentence describes the member's DM separately from
// enforcement.
func notificationDeliverySentence(status string) string {
	switch status {
	case "sent":
		return "The member was sent a DM."
	case "failed":
		return "The member’s DM couldn’t be delivered."
	case "pending", "prepared", "claimed":
		return "The member’s DM is queued."
	case "sending", "running":
		return "The member’s DM is being sent."
	case "skipped":
		return "No DM was sent to the member."
	default:
		return "The member’s DM status is **" + PlainText(strings.ReplaceAll(status, "_", " ")) + "**."
	}
}

// staffActionSummary describes each action and its last failure in
// sentences.
func staffActionSummary(actions []quack.CaseActionDetailResponse) string {
	if len(actions) == 0 {
		return "Warning recorded."
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := actionSentence(action.ActionType, action.Status, action.TimeoutUntil)
		if action.LastErrorCode != "" {
			row += "\n" + safeFailure(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// actionSentence is ActionSentence with the confirmed end of a succeeded
// timeout, written as Discord timestamps so each reader sees it in their
// own time zone.
func actionSentence(action quack.ActionType, status quack.ActionExecutionStatus, timeoutUntil *time.Time) string {
	if action == quack.ActionTimeoutUser && status == quack.ActionExecutionSucceeded && timeoutUntil != nil {
		return fmt.Sprintf("Timed out until <t:%d:f> (<t:%d:R>).", timeoutUntil.Unix(), timeoutUntil.Unix())
	}
	return ActionSentence(action, status)
}

// contextSummary quotes each staff-visible context value, escaped.
func contextSummary(values []quack.CaseContextValueResponse) string {
	rows := make([]string, 0, len(values))
	for _, value := range values {
		if value.Value == nil {
			continue
		}
		rows = append(rows, Quote(PlainText(value.Label)+" — "+PlainText(fmt.Sprint(value.Value))))
	}
	return strings.Join(rows, "\n")
}

// eventSummary shows the six most recent case events with their times.
func eventSummary(events []quack.CaseEventResponse) string {
	events = events[max(len(events)-6, 0):]
	rows := make([]string, 0, len(events))
	for _, event := range events {
		rows = append(rows, "-# "+strings.TrimSpace(RelativeTime(event.CreatedAt)+" · "+PlainText(event.Body)))
	}
	return strings.Join(rows, "\n")
}

// safeFailure turns an error code like "ban_rate_limited" into short words,
// never Discord's raw response.
func safeFailure(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Discord action failed"
	}
	return PlainText(strings.ReplaceAll(Truncate(strings.TrimSpace(code), 160), "_", " "))
}

// historicalCaseLabel names what an imported v4 case recorded, without
// inventing an execution result. Imported metadata is display-only.
func historicalCaseLabel(item quack.CaseResponse) string {
	metadata, _ := item.Metadata.(map[string]any)
	legacy, _ := metadata["v4"].(map[string]any)
	action, _ := legacy["action_type"].(string)
	switch action {
	case "warning":
		return "Warning"
	case "ban":
		return "Ban"
	case "kick":
		return "Kick"
	case "unban":
		return "Unban"
	case "timeout":
		return "Timeout"
	case "message_delete":
		return "Message deletion"
	default:
		return "Historical case"
	}
}

// historicalContextLink links an imported case's original https source,
// escaped so the URL cannot break out of the Markdown link.
func historicalContextLink(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil {
		return ""
	}
	safe := strings.NewReplacer("(", "%28", ")", "%29", "<", "%3C", ">", "%3E", "\n", "%0A", "\r", "%0D").Replace(parsed.String())
	return "[View original context](" + safe + ")"
}
