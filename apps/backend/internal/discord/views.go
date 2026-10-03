package discord

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// caseCreatedMessage announces a new case in the channel it was created
// from: who, which rule, what Quack did about it, and the context staff
// gave. Member notifications have their own wording.
func caseCreatedMessage(created *quack.CaseResponse, template *quack.TemplateResponse) Message {
	if created == nil {
		return Signal("case_add", "Case added.", false)
	}
	meta := []string{fmt.Sprintf("Case #%d", created.CaseNumber)}
	if date := RelativeTime(created.CreatedAt); date != "" {
		meta = append(meta, date)
	}
	icon := "case_add"
	if len(created.Actions) == 0 {
		icon = "warn"
	}
	for _, action := range created.Actions {
		if action.Status == quack.ActionExecutionFailed {
			icon = "error"
			break
		}
		if action.Status == quack.ActionExecutionPending || action.Status == quack.ActionExecutionRunning ||
			action.Status == quack.ActionExecutionRetrying {
			icon = "pending"
		}
	}
	status := publicActionStatus(created.Actions)
	if created.ModeratorDiscordUserID != "" {
		status += "\nModerator: <@" + created.ModeratorDiscordUserID + ">"
	}
	if context := contextSummary(created.ContextValues); context != "" {
		status += "\n\n" + context
	}
	lead := fmt.Sprintf("Case #%d · <@%s>", created.CaseNumber, created.TargetDiscordUserID)
	if name := templateName(template); name != "" {
		lead += " · **" + PlainText(name) + "**"
	}
	if created.Validity == quack.CaseValidityVoided {
		lead = "**Voided** · " + lead
	}
	message := Conversation(icon, lead, PlainText(created.Reason), status, strings.Join(meta, " · "), false)
	message.Components = []discordgo.MessageComponent{Row(casePrimaryControls(created.ID, created.Validity == quack.CaseValidityVoided)...)}
	return message
}

// caseVoidedMessage confirms a void, and says when a punishment is still
// being applied or removed.
func caseVoidedMessage(item *quack.CaseResponse) Message {
	status := "It stays in history and no longer counts toward escalation."
	for _, action := range item.Actions {
		if action.ActionType == quack.ActionRemoveTimeout || action.ActionType == quack.ActionUnbanUser {
			status += "\n" + publicActionStatus([]quack.CaseActionResponse{action})
		} else if action.Status == quack.ActionExecutionRunning {
			status += "\nEnforcement is still finishing. Quack will try to undo any ban or timeout that succeeds."
		}
	}
	return Conversation("case_void", fmt.Sprintf("Case #%d was voided.", item.CaseNumber), "", status, "", false)
}

// publicActionStatus describes each action's progress, never calling a
// queued action done.
func publicActionStatus(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return "Warning recorded."
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, ActionSentence(action.ActionType, action.Status))
	}
	return strings.Join(parts, "\n")
}

// templateName prefers the admin-facing name and falls back to the slug.
func templateName(template *quack.TemplateResponse) string {
	if template == nil {
		return ""
	}
	if name := strings.TrimSpace(template.Name); name != "" {
		return name
	}
	return strings.TrimSpace(template.Slug)
}

// casePrimaryControls are the controls every case message carries.
func casePrimaryControls(caseID string, voided bool) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{caseButton("void", caseID, "Void case", discordgo.DangerButton, voided)}
}

// caseButton returns a button routed to the case namespace.
func caseButton(action, payload, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	return Button(MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: payload}), label, style, disabled)
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
	rows := []discordgo.MessageComponent{Row(casePrimaryControls(detail.ID, detail.Validity == quack.CaseValidityVoided)...)}
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

// caseListMessage renders one page of cases, guild-wide or for targetID,
// with Prev and Next buttons whose payload is "page|target".
func caseListMessage(list *quack.CaseListResponse, page int, targetID string) Message {
	return boundedCaseHistoryMessage(list, page, targetID, "")
}

// caseProfileMessage is a member's case history page with their all-time
// totals underneath.
func caseProfileMessage(profile *quack.CaseProfileResponse, page int, targetID string) Message {
	if profile == nil {
		return caseListMessage(nil, page, targetID)
	}
	summary := fmt.Sprintf("\n-# %d total · %d active · %d voided", profile.Summary.Total,
		profile.Summary.ByValidity[string(quack.CaseValidityValid)], profile.Summary.ByValidity[string(quack.CaseValidityVoided)])
	return boundedCaseHistoryMessage(&profile.CaseListResponse, page, targetID, summary)
}

// boundedCaseHistoryMessage fits a whole page in one message by shortening
// only the row labels: every row and control stays. Each icon placeholder
// reserves room for the emoji it becomes.
func boundedCaseHistoryMessage(list *quack.CaseListResponse, page int, targetID, summary string) Message {
	for limit := 100; ; limit-- {
		message := caseHistoryMessage(list, page, targetID, limit)
		message.Content += summary
		units := utf16Len(message.Content) + 64*strings.Count(message.Content, "{{quack:")
		if units <= contentLimit || limit == 0 {
			return message
		}
	}
}

// caseHistoryMessage renders a page of cases with row labels cut to
// labelLimit runes.
func caseHistoryMessage(list *quack.CaseListResponse, page int, targetID string, labelLimit int) Message {
	page = max(page, 1)
	var rows []string
	var total int64
	if list != nil {
		total = list.Total
		for _, item := range list.Cases {
			row := fmt.Sprintf("**#%d**  <@%s> · %s", item.CaseNumber, item.TargetDiscordUserID, caseRowLabel(item, labelLimit))
			if item.Source == quack.CaseSourceV4Import {
				row = fmt.Sprintf("**#%d**  <@%s> · %s · **Imported v4**", item.CaseNumber, item.TargetDiscordUserID, historicalCaseLabel(item))
			}
			if item.Validity == quack.CaseValidityVoided {
				row += " · **Voided**"
			}
			if date := RelativeTime(item.CreatedAt); date != "" {
				row += "\n-# " + date
			}
			rows = append(rows, row)
		}
	}
	totalPages := pageCount(total)
	prefix := "list"
	if targetID != "" {
		prefix = "user"
	}
	components, _ := Pagination("case", prefix, fmt.Sprintf("%d|%s", page, targetID), page, totalPages)
	lead := "Here are the latest cases."
	if targetID != "" {
		lead = "Here’s the case history for <@" + targetID + ">."
	}
	if total == 0 {
		lead = "No cases yet."
		if targetID != "" {
			lead = "No cases found for <@" + targetID + ">."
		}
		rows = nil
	}
	message := Conversation("history", lead, "", strings.Join(rows, "\n\n"), fmt.Sprintf("Page %d/%d · %d total", page, totalPages, total), false)
	message.Components = components
	return message
}

// caseRowLabel names a case in a list by its selected level, cut to limit
// runes.
func caseRowLabel(item quack.CaseResponse, limit int) string {
	if item.SelectedLevel == nil || strings.TrimSpace(item.SelectedLevel.Name) == "" {
		return "Case recorded"
	}
	name := item.SelectedLevel.Name
	label := PlainText(Truncate(name, limit))
	if len([]rune(name)) > limit {
		label += "…"
	}
	return label
}

// failedActionMessage renders the queue of failed actions awaiting review,
// with Retry, Dismiss, and Void controls for the first one.
func failedActionMessage(result *quack.FailedCaseActionResult, page int) Message {
	page = max(page, 1)
	var rows []string
	var components []discordgo.MessageComponent
	var total int64
	if result != nil {
		total = result.Total
		for index, item := range result.Executions {
			rows = append(rows, fmt.Sprintf("`%s` · %s · %s", item.ID, item.ActionType.Label(), safeFailure(item.LastErrorCode)))
			if index == 0 {
				components = append(components, Row(
					caseButton("retry", item.ID, "Retry first", discordgo.SecondaryButton, false),
					caseButton("dismiss", item.ID, "Dismiss first", discordgo.SecondaryButton, false),
					caseButton("void", item.CaseID, "Void case", discordgo.DangerButton, false),
				))
			}
		}
	}
	totalPages := pageCount(total)
	pages, _ := Pagination("case", "failures", fmt.Sprintf("%d", page), page, totalPages)
	components = append(components, pages...)
	icon, lead := "success", "No action failures need review."
	if len(rows) > 0 {
		icon, lead = "error", "These actions need a hand."
	}
	message := Conversation(icon, lead, "", strings.Join(rows, "\n\n"), fmt.Sprintf("Page %d/%d · %d active", page, totalPages, total), false)
	message.Components = components
	return message
}

// pageCount returns how many pages total items fill, at least one.
func pageCount(total int64) int {
	return max(int((total+casePageSize-1)/casePageSize), 1)
}

// staffActionSummary describes each action and its last failure in
// sentences.
func staffActionSummary(actions []quack.CaseActionDetailResponse) string {
	if len(actions) == 0 {
		return "Warning recorded."
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := ActionSentence(action.ActionType, action.Status)
		if action.LastErrorCode != "" {
			row += "\n" + safeFailure(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

// contextSummary quotes each staff-visible context value, escaped.
func contextSummary(values []quack.CaseContextValueResponse) string {
	rows := make([]string, 0, len(values))
	for _, value := range values {
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
