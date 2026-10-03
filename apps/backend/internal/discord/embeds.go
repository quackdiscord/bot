package discord

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// caseCreatedMessage is the public result of a new case. It shows the case
// number, target, rule, level, and action status, and deliberately leaves
// out the moderator, context, and evidence.
func caseCreatedMessage(created *quack.CaseResponse, template *quack.TemplateResponse) Message {
	if created == nil {
		return Content("Created case.", false)
	}
	lines := []string{fmt.Sprintf("**Case #%d created** · <@%s>", created.CaseNumber, created.TargetDiscordUserID)}
	if name := templateName(template); name != "" {
		lines = append(lines, "**Template:** "+Truncate(name, 256))
	}
	if level := created.SelectedLevel; level != nil {
		name := strings.TrimSpace(level.Name)
		if name == "" {
			name = fmt.Sprintf("Level %d", level.Position)
		}
		lines = append(lines, "**Level:** "+Truncate(name, 256))
	}
	lines = append(lines, "**Action:** "+publicActionStatus(created.Actions))
	return Content(Truncate(strings.Join(lines, "\n"), 2000), false)
}

func publicActionStatus(actions []quack.CaseActionResponse) string {
	if len(actions) == 0 {
		return "No Discord action configured"
	}
	parts := make([]string, 0, len(actions))
	for _, action := range actions {
		parts = append(parts, fmt.Sprintf("%s · %s", action.ActionType.Label(), action.Status.Label()))
	}
	return strings.Join(parts, ", ")
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

// caseDetailMessage is the staff view of one case, with validity,
// enforcement, appeal eligibility, context, evidence, and history in
// separate fields, plus void, retry, dismiss, or reverse controls.
func caseDetailMessage(detail *quack.CaseDetailResponse) Message {
	if detail == nil {
		return embedMessage(warningEmbed("Case", "Case not found."), true)
	}
	e := newEmbed(fmt.Sprintf("Case #%d", detail.CaseNumber), detail.Reason, colorMain).
		field("Target", "<@"+detail.TargetDiscordUserID+">", true).
		field("Validity", detail.Validity, true).
		field("Source", detail.Source, true)
	if detail.SelectedLevel != nil {
		e.field("Selected outcome", detail.SelectedLevel.Name, true)
	}
	if detail.TemplateSnapshot != nil {
		appeal := "Not eligible"
		if detail.TemplateSnapshot.Template.Appealable {
			appeal = "Eligible"
		}
		e.field("Appeal", appeal, true)
	}
	e.field("Enforcement", staffActionSummary(detail.Actions), false)
	if detail.Notification != nil {
		e.field("Notification", detail.Notification.Status, true)
	}
	if summary := contextSummary(detail.ContextValues); summary != "" {
		e.field("Visible context", summary, false)
	}
	if summary := evidenceSummary(detail.Evidence); summary != "" {
		e.field("Evidence", summary, false)
	}
	if summary := eventSummary(detail.Events); summary != "" {
		e.field("History", summary, false)
	}
	return Message{Embeds: []*discordgo.MessageEmbed{e.build()}, Components: caseDetailControls(detail)}
}

// caseDetailControls offers Void, plus Retry and Dismiss for the first
// failed action or Reverse for the first succeeded timeout or ban.
func caseDetailControls(detail *quack.CaseDetailResponse) []discordgo.MessageComponent {
	button := func(action, payload, label string, style discordgo.ButtonStyle) discordgo.MessageComponent {
		return Button(MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1", Payload: payload}), label, style, false)
	}
	void := Button(MustCustomID(CustomID{Namespace: "case", Action: "void", Version: "v1", Payload: detail.ID}),
		"Void case", discordgo.DangerButton, detail.Validity == quack.CaseValidityVoided)
	buttons := []discordgo.MessageComponent{void}
	for _, action := range detail.Actions {
		if action.Status == quack.ActionExecutionFailed {
			buttons = append(buttons,
				button("retry", action.ID, "Retry", discordgo.PrimaryButton),
				button("dismiss", action.ID, "Dismiss", discordgo.SecondaryButton),
			)
			break
		}
		if action.Status == quack.ActionExecutionSucceeded && (action.ActionType == quack.ActionTimeoutUser || action.ActionType == quack.ActionBanUser) {
			reversal := quack.ActionRemoveTimeout
			if action.ActionType == quack.ActionBanUser {
				reversal = quack.ActionUnbanUser
			}
			payload := strings.Join([]string{detail.ID, action.ID, string(reversal)}, "|")
			buttons = append(buttons, button("reverse", payload, "Reverse action", discordgo.SecondaryButton))
			break
		}
	}
	return []discordgo.MessageComponent{Row(buttons...)}
}

// caseListMessage renders one page of cases, guild-wide or for targetID,
// with Prev and Next buttons whose payload is "page|target".
func caseListMessage(list *quack.CaseListResponse, page int, targetID string) Message {
	page = max(page, 1)
	var rows []string
	var total int64
	if list != nil {
		total = list.Total
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
	totalPages := pageCount(total)
	title, prefix := "Recent Cases", "list"
	if targetID != "" {
		title, prefix = "Case History for <@"+targetID+">", "user"
	}
	components, _ := pagination("case", prefix, fmt.Sprintf("%d|%s", page, targetID), page, totalPages)
	e := newEmbed(title, strings.Join(rows, "\n"), colorMain).
		footer(fmt.Sprintf("Page %d/%d · %d total", page, totalPages, total))
	return Message{Embeds: []*discordgo.MessageEmbed{e.build()}, Components: components}
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
			rows = append(rows, fmt.Sprintf("`%s` · %s · %s", item.ID, item.ActionType.Label(), failureLabel(item.LastErrorCode)))
			if index > 0 {
				continue
			}
			retryID := MustCustomID(CustomID{Namespace: "case", Action: "retry", Version: "v1", Payload: item.ID})
			dismissID := MustCustomID(CustomID{Namespace: "case", Action: "dismiss", Version: "v1", Payload: item.ID})
			voidID := MustCustomID(CustomID{Namespace: "case", Action: "void", Version: "v1", Payload: item.CaseID})
			components = append(components, Row(
				Button(retryID, "Retry first", discordgo.PrimaryButton, false),
				Button(dismissID, "Dismiss first", discordgo.SecondaryButton, false),
				Button(voidID, "Void case", discordgo.DangerButton, false),
			))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, "No action failures need review.")
	}
	totalPages := pageCount(total)
	pages, _ := pagination("case", "failures", fmt.Sprintf("%d", page), page, totalPages)
	components = append(components, pages...)
	e := newEmbed("Failed Actions", strings.Join(rows, "\n"), colorError).
		footer(fmt.Sprintf("Page %d/%d · %d active", page, totalPages, total))
	return Message{Embeds: []*discordgo.MessageEmbed{e.build()}, Components: components}
}

// pageCount returns how many pages total items fill, at least one.
func pageCount(total int64) int {
	return max(int((total+casePageSize-1)/casePageSize), 1)
}

func staffActionSummary(actions []quack.CaseActionDetailResponse) string {
	if len(actions) == 0 {
		return "No Discord action configured"
	}
	rows := make([]string, 0, len(actions))
	for _, action := range actions {
		row := fmt.Sprintf("%s · %s · %d attempt(s)", action.ActionType.Label(), action.Status.Label(), action.AttemptCount)
		if action.LastErrorCode != "" {
			row += " · " + failureLabel(action.LastErrorCode)
		}
		rows = append(rows, row)
	}
	return strings.Join(rows, "\n")
}

func contextSummary(values []quack.CaseContextValueResponse) string {
	rows := make([]string, 0, len(values))
	for _, value := range values {
		rows = append(rows, fmt.Sprintf("**%s:** %s", value.Label, Truncate(fmt.Sprint(value.Value), 180)))
	}
	return strings.Join(rows, "\n")
}

// evidenceSummary lists each linked message and its attachments, linking the
// preserved copy when there is one.
func evidenceSummary(evidence []quack.CaseEvidenceResponse) string {
	var rows []string
	for _, item := range evidence {
		label := item.MessageURL
		if label == "" {
			label = item.CaptureOutcome
		}
		rows = append(rows, label)
		for _, attachment := range item.Attachments {
			link := attachment.PreservedURL
			if link == "" {
				link = attachment.OriginalURL
			}
			rows = append(rows, fmt.Sprintf("[%s](%s) · %s", attachment.Filename, link, attachment.CopyOutcome))
		}
	}
	return strings.Join(rows, "\n")
}

// eventSummary shows the six most recent case events.
func eventSummary(events []quack.CaseEventResponse) string {
	events = events[max(len(events)-6, 0):]
	rows := make([]string, 0, len(events))
	for _, event := range events {
		rows = append(rows, fmt.Sprintf("%s · %s", event.EventType, event.Body))
	}
	return strings.Join(rows, "\n")
}

// failureLabel turns an error code like "ban_rate_limited" into words.
func failureLabel(code string) string {
	if strings.TrimSpace(code) == "" {
		return "Discord action failed"
	}
	return strings.ReplaceAll(strings.TrimSpace(code), "_", " ")
}

// appealStaffMessage is the staff view of an appeal: its timeline and a
// confirmation button for each reversal Quack offers after acceptance. The
// button's payload is "appeal,execution,action".
func appealStaffMessage(appeal *quack.AppealResponse) Message {
	if appeal == nil {
		return embedMessage(errorEmbed("Appeal not found."), true)
	}
	e := newEmbed("Appeal Review", "", colorMain).
		field("Case", appeal.CaseID, true).
		field("Member", fmt.Sprintf("<@%s>", appeal.TargetDiscordUserID), true).
		field("Status", appeal.Status, true).
		footer("Appeal ID: " + appeal.ID)
	for _, event := range appeal.Events {
		actor := event.ActorType
		if event.ActorDiscordUserID != "" {
			actor += " <@" + event.ActorDiscordUserID + ">"
		}
		e.field(string(event.Type), actor+": "+event.Body, false)
	}
	message := embedMessage(e.build(), false)
	for _, offer := range appeal.ReversalOffers {
		payload := appeal.ID + "," + offer.OriginalExecutionID + "," + string(offer.ActionType)
		customID, err := EncodeCustomID(CustomID{Namespace: "appeal", Action: "reverse", Version: "v1", Payload: payload})
		if err != nil {
			continue
		}
		label := "Confirm " + strings.ToLower(offer.ActionType.Label())
		message.Components = append(message.Components, Row(Button(customID, label, discordgo.DangerButton, false)))
	}
	return message
}

// appealEntryMessage links an appealable case notification to the case's
// appeal page in the dashboard. Only an https dashboard is accepted, and any
// query or fragment on it is dropped.
func appealEntryMessage(baseURL, guildID, caseID string) (Message, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return Message{}, fmt.Errorf("secure dashboard base URL is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/guilds/" + url.PathEscape(guildID) + "/cases/" + url.PathEscape(caseID) + "/appeal"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return Message{
		Content:    "This case is eligible for appeal.",
		Components: []discordgo.MessageComponent{Row(linkButton(parsed.String(), "Open appeal"))},
	}, nil
}
