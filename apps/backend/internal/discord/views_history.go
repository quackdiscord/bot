package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

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
// only the rule names: every row and control stays. Each icon placeholder
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

// caseHistoryMessage renders a page of cases with rule names cut to
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

// caseRowLabel names a case in a list by its rule, cut to limit runes.
// Escalation level names mean little out of context, so they are left for
// the case detail.
func caseRowLabel(item quack.CaseResponse, limit int) string {
	name := strings.TrimSpace(item.RuleName)
	if name == "" {
		return "Case recorded"
	}
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
