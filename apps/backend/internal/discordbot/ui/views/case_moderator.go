package views

import (
	"fmt"
	"math"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
)

const casePageSize = 10

// CaseDetailMessage renders authorized staff detail with validity, enforcement,
// appeal availability, visible context, evidence, and immutable history separated.
func CaseDetailMessage(detail *quack.CaseDetailResponse) ui.Message {
	if detail == nil {
		return ui.EmbedMessage(ui.WarningEmbed("Case", "Case not found."), true)
	}
	embed := ui.NewInfoEmbed(fmt.Sprintf("Case #%d", detail.CaseNumber), detail.Reason).
		AddField("Target", "<@"+detail.TargetDiscordUserID+">", true).
		AddField("Validity", detail.Validity, true).
		AddField("Source", detail.Source, true)
	if detail.SelectedLevel != nil {
		embed.AddField("Selected outcome", detail.SelectedLevel.Name, true)
	}
	if detail.TemplateSnapshot != nil {
		state := "Not eligible"
		if detail.TemplateSnapshot.Template.Appealable {
			state = "Eligible"
		}
		embed.AddField("Appeal", state, true)
	}
	embed.AddField("Enforcement", staffActionSummary(detail.Actions), false)
	if detail.Notification != nil {
		embed.AddField("Notification", detail.Notification.Status, true)
	}
	if context := contextSummary(detail.ContextValues); context != "" {
		embed.AddField("Visible context", context, false)
	}
	if evidence := evidenceSummary(detail.Evidence); evidence != "" {
		embed.AddField("Evidence", evidence, false)
	}
	if history := eventSummary(detail.Events); history != "" {
		embed.AddField("History", history, false)
	}
	components := caseDetailComponents(detail)
	return ui.Message{Embeds: []*discordgo.MessageEmbed{embed.Build()}, Components: components, Ephemeral: false}
}

// CaseListMessage renders one stable case page and its navigation controls.
func CaseListMessage(list *quack.CaseListResponse, page int, targetID string) ui.Message {
	if page < 1 {
		page = 1
	}
	rows := make([]string, 0)
	if list != nil {
		for _, item := range list.Cases {
			level := ""
			if item.SelectedLevel != nil {
				level = " · " + item.SelectedLevel.Name
			}
			rows = append(rows, fmt.Sprintf("**#%d** · <@%s> · %s%s", item.CaseNumber, item.TargetDiscordUserID, item.Validity, level))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No cases found.")
	}
	total := int64(0)
	if list != nil {
		total = list.Total
	}
	totalPages := int(math.Ceil(float64(total) / casePageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	payload := fmt.Sprintf("%d|%s", page, targetID)
	prefix := "list"
	if targetID != "" {
		prefix = "user"
	}
	components, _ := ui.Pagination("case", prefix, payload, page, totalPages)
	title := "Recent Cases"
	if targetID != "" {
		title = "Case History for <@" + targetID + ">"
	}
	embed := ui.NewInfoEmbed(title, strings.Join(rows, "\n")).SetFooter(fmt.Sprintf("Page %d/%d · %d total", page, totalPages, total)).Build()
	return ui.Message{Embeds: []*discordgo.MessageEmbed{embed}, Components: components, Ephemeral: false}
}

// FailedActionMessage renders the active recovery queue with real retry, dismiss, and void controls.
func FailedActionMessage(result *quack.FailedCaseActionResult, page int) ui.Message {
	if page < 1 {
		page = 1
	}
	rows := []string{}
	components := []discordgo.MessageComponent{}
	if result != nil {
		for index, item := range result.Executions {
			rows = append(rows, fmt.Sprintf("`%s` · %s · %s", item.ID, item.ActionType.Label(), safeFailure(item.LastErrorCode)))
			if index == 0 {
				retryID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: item.ID})
				dismissID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "dismiss", Version: "v1", Payload: item.ID})
				voidID := ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void", Version: "v1", Payload: item.CaseID})
				components = append(components, ui.Row(ui.Button(retryID, "Retry first", discordgo.PrimaryButton, false), ui.Button(dismissID, "Dismiss first", discordgo.SecondaryButton, false), ui.Button(voidID, "Void case", discordgo.DangerButton, false)))
			}
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No action failures need review.")
	}
	total := int64(0)
	if result != nil {
		total = result.Total
	}
	totalPages := int(math.Ceil(float64(total) / casePageSize))
	if totalPages < 1 {
		totalPages = 1
	}
	pagination, _ := ui.Pagination("case", "failures", fmt.Sprintf("%d", page), page, totalPages)
	components = append(components, pagination...)
	embed := ui.NewErrorEmbed(strings.Join(rows, "\n")).SetTitle("Failed Actions").SetFooter(fmt.Sprintf("Page %d/%d · %d active", page, totalPages, total)).Build()
	return ui.Message{Embeds: []*discordgo.MessageEmbed{embed}, Components: components, Ephemeral: false}
}

func caseDetailComponents(detail *quack.CaseDetailResponse) []discordgo.MessageComponent {
	buttons := []discordgo.MessageComponent{ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "void", Version: "v1", Payload: detail.ID}), "Void case", discordgo.DangerButton, detail.Validity == quack.CaseValidityVoided)}
	for _, action := range detail.Actions {
		if action.Status == quack.ActionExecutionFailed {
			buttons = append(buttons, ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: action.ID}), "Retry", discordgo.PrimaryButton, false), ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "dismiss", Version: "v1", Payload: action.ID}), "Dismiss", discordgo.SecondaryButton, false))
			break
		}
		if action.Status == quack.ActionExecutionSucceeded && (action.ActionType == quack.ActionTimeoutUser || action.ActionType == quack.ActionBanUser) {
			reversal := quack.ActionRemoveTimeout
			if action.ActionType == quack.ActionBanUser {
				reversal = quack.ActionUnbanUser
			}
			payload := strings.Join([]string{detail.ID, action.ID, string(reversal)}, "|")
			buttons = append(buttons, ui.Button(ui.MustCustomID(ui.CustomID{Namespace: "case", Action: "reverse", Version: "v1", Payload: payload}), "Reverse action", discordgo.SecondaryButton, false))
			break
		}
	}
	return []discordgo.MessageComponent{ui.Row(buttons...)}
}

func staffActionSummary(actions []quack.CaseActionDetailResponse) string {
	if len(actions) == 0 {
		return "No Discord action configured"
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := fmt.Sprintf("%s · %s · %d attempt(s)", action.ActionType.Label(), action.Status.Label(), action.AttemptCount)
		if action.LastErrorCode != "" {
			row += " · " + safeFailure(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func contextSummary(values []quack.CaseContextValueResponse) string {
	rows := make([]string, 0, len(values))
	for _, value := range values {
		rows = append(rows, fmt.Sprintf("**%s:** %s", value.Label, ui.TruncateRunes(fmt.Sprint(value.Value), 180)))
	}
	return strings.Join(rows, "\n")
}

func evidenceSummary(evidence []quack.CaseEvidenceResponse) string {
	rows := make([]string, 0, len(evidence))
	for _, item := range evidence {
		label := item.MessageURL
		if label == "" {
			label = item.CaptureOutcome
		}
		rows = append(rows, label)
		for _, attachment := range item.Attachments {
			url := attachment.PreservedURL
			if url == "" {
				url = attachment.OriginalURL
			}
			rows = append(rows, fmt.Sprintf("[%s](%s) · %s", attachment.Filename, url, attachment.CopyOutcome))
		}
	}
	return strings.Join(rows, "\n")
}

func eventSummary(events []quack.CaseEventResponse) string {
	start := 0
	if len(events) > 6 {
		start = len(events) - 6
	}
	rows := make([]string, 0, len(events)-start)
	for _, event := range events[start:] {
		rows = append(rows, fmt.Sprintf("%s · %s", event.EventType, event.Body))
	}
	return strings.Join(rows, "\n")
}

func safeFailure(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Discord action failed"
	}
	return strings.ReplaceAll(strings.TrimSpace(code), "_", " ")
}
