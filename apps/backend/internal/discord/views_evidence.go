package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// evidencePages splits one evidence item, after the case's context, into
// text pages. Icons are resolved for applicationID first, since they count
// toward Discord's limit.
func evidencePages(page *quack.CaseEvidencePage, applicationID string) []string {
	var evidence []quack.CaseEvidenceResponse
	if page.Evidence != nil {
		evidence = []quack.CaseEvidenceResponse{*page.Evidence}
	}
	body := evidenceSummary(evidence)
	if body == "" {
		body = "No evidence has been added yet."
	}
	if context := contextSummary(page.ContextValues); context != "" {
		body = context + "\n\n" + body
	}
	return TextPages(discordtext.Resolve(body, applicationID), 1600)
}

// evidencePageMessage shows text page textPage (1-based, clamped) of one
// evidence item. Previous and Next step through the item's text pages and
// on into the neighboring items, with the payload "position:page|case".
func evidencePageMessage(evidence *quack.CaseEvidencePage, textPage int, applicationID string) Message {
	if evidence == nil {
		return Signal("error", "That case could not be found.", true)
	}
	pages := evidencePages(evidence, applicationID)
	textPage = min(max(textPage, 1), len(pages))
	body := pages[textPage-1] + fmt.Sprintf("\n\nAdd a screenshot with `/case evidence case:%d file:` or use its `message_link` option.", evidence.CaseNumber)
	footer := ""
	if evidence.Total > 0 {
		footer = fmt.Sprintf("Evidence %d of %d", evidence.Position, evidence.Total)
	}
	if len(pages) > 1 {
		footer += fmt.Sprintf(" · page %d of %d", textPage, len(pages))
	}
	message := Conversation("evidence", fmt.Sprintf("Evidence for case #%d", evidence.CaseNumber), "", body, footer, false)
	previous := evidence.Position > 1 || textPage > 1
	next := int64(evidence.Position) < evidence.Total || textPage < len(pages)
	if previous || next {
		payload := fmt.Sprintf("%d:%d|%s", evidence.Position, textPage, evidence.CaseID)
		message.Components = []discordgo.MessageComponent{Row(
			caseButton("evidence_prev", payload, "Previous", discordgo.SecondaryButton, !previous),
			caseButton("evidence_next", payload, "Next", discordgo.SecondaryButton, !next),
		)}
	}
	return message
}

// evidenceSummary lists each piece of evidence with a descriptive link, its
// quoted text, its files, and any capture warning in plain words.
func evidenceSummary(evidence []quack.CaseEvidenceResponse) string {
	var rows []string
	for _, item := range evidence {
		label := evidenceCaptureLabel(item.CaptureOutcome)
		if item.MessageURL != "" {
			label = "[View message](" + item.MessageURL + ")"
		}
		rows = append(rows, label)
		if item.Content != "" {
			rows = append(rows, Quote(PlainText(item.Content)))
		}
		if warning := evidenceSnapshotWarning(item); warning != "" {
			rows = append(rows, "{{quack:warn}} "+PlainText(warning))
		}
		for _, attachment := range item.Attachments {
			link := attachment.PreservedURL
			if link == "" {
				link = attachment.OriginalURL
			}
			rows = append(rows, fmt.Sprintf("[%s](%s) · %s", PlainText(attachment.Filename), link, evidenceCopyLabel(attachment.CopyOutcome)))
			if attachment.Warning != "" {
				rows = append(rows, "{{quack:warn}} "+PlainText(evidenceWarningCopy(attachment.Warning)))
			}
		}
	}
	return strings.Join(rows, "\n")
}

// evidenceCopyLabel describes the outcomes managed copying produces. Any
// other value cannot prove a durable copy exists.
func evidenceCopyLabel(outcome string) string {
	switch outcome {
	case "preserved":
		return "Saved copy"
	case "metadata_only":
		return "No confirmed copy"
	default:
		return "Copy status unavailable"
	}
}

// evidenceSnapshotWarning returns the capture warning without the parts
// repeated under individual attachments, translated where Quack knows the
// wording.
func evidenceSnapshotWarning(item quack.CaseEvidenceResponse) string {
	warning := "; " + strings.TrimSpace(item.CaptureWarning) + "; "
	for _, attachment := range item.Attachments {
		if attachment.Warning == "" {
			continue
		}
		duplicate := "; " + attachment.Warning + "; "
		for strings.Contains(warning, duplicate) {
			warning = strings.Replace(warning, duplicate, "; ", 1)
		}
	}
	warning = strings.TrimSuffix(strings.TrimPrefix(warning, "; "), "; ")
	if warning == "" {
		return ""
	}
	if mapped := evidenceWarningCopy(warning); mapped != warning {
		return mapped
	}
	// Capture joins independent warnings with "; ". Translate the known
	// ones and keep unknown text as it is.
	parts := strings.Split(warning, "; ")
	for i := range parts {
		parts[i] = evidenceWarningCopy(parts[i])
	}
	return strings.Join(parts, "; ")
}

// evidenceWarningCopy translates a known capture warning into staff
// wording. Unknown warnings are shown as they are rather than hidden.
func evidenceWarningCopy(warning string) string {
	switch warning {
	case "managed evidence channel is unavailable":
		return "The evidence channel is unavailable. Ask an administrator to check it."
	case "attachment exceeds the managed copy size limit":
		return "This file is too large to save."
	case "attachment type is not eligible for managed copying":
		return "This file type cannot be saved."
	case "attachment copy failed; original metadata retained":
		return "Quack could not save this file. The original link may stop working."
	case "attachment copy could not be confirmed; original metadata retained":
		return "Quack could not confirm a saved copy. The original link may stop working."
	case "message content snapshot was truncated":
		return "Only part of the message text was saved."
	case "embed snapshot was truncated":
		return "Some message embeds were not saved."
	case "attachment snapshot was truncated", "total attachment snapshot limit reached", "total attachment snapshot was truncated":
		return "Some files were not saved because the capture limit was reached."
	case "linked message was deleted or does not exist":
		return "The original message was deleted or could not be found."
	case "linked message is unavailable":
		return "The original message is unavailable."
	default:
		return warning
	}
}

// evidenceCaptureLabel heads evidence that has no message link.
func evidenceCaptureLabel(outcome string) string {
	switch outcome {
	case "uploaded":
		return "Uploaded file"
	case "captured":
		return "Captured message"
	case "unavailable":
		return "Capture unavailable"
	case "deleted":
		return "Message deleted or missing"
	case "inaccessible":
		return "Message inaccessible"
	default:
		return "Evidence"
	}
}
