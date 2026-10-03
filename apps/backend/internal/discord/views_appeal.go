package discord

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// statementPageLimit is how much of a resolved appeal fits on one page,
// leaving room for the page line and Discord's 2000-unit limit.
const statementPageLimit = 1700

// appealStaffPage is page page of the staff view of appeal. A long statement
// is split into pages with Prev and Next buttons instead of an attachment,
// and every page keeps the decision controls. Icons are resolved for
// applicationID first, so pages are measured as Discord will count them.
func appealStaffPage(appeal *quack.AppealResponse, page int, applicationID string) Message {
	message := appealStaffMessage(appeal)
	if appeal == nil {
		return message
	}
	pages := TextPages(discordtext.Resolve(message.Content, applicationID), statementPageLimit)
	page = max(1, min(page, len(pages)))
	message.Content = pages[page-1]
	if len(pages) > 1 {
		message.Content += fmt.Sprintf("\n\n-# Case #%d · Statement page %d/%d", appeal.CaseNumber, page, len(pages))
		controls, err := Pagination(appealNamespace, "statement", strconv.Itoa(page)+"|"+appeal.ID, page, len(pages))
		if err == nil {
			message.Components = append(message.Components, controls...)
		}
	}
	return message
}

// appealStaffMessage is the staff view of an appeal: who appealed, where it
// stands, the decision reason, the member's quoted statement, Accept and
// Reject while it is pending, and a confirmation button for each reversal
// still on offer after acceptance.
func appealStaffMessage(appeal *quack.AppealResponse) Message {
	if appeal == nil {
		return Signal("error", "That appeal couldn’t be found.", true)
	}
	lead := fmt.Sprintf("Received an appeal from <@%s>.", appeal.TargetDiscordUserID)
	switch appeal.Status {
	case quack.AppealStatusAccepted, quack.AppealStatusRejected:
		lead = fmt.Sprintf("Appeal %s · <@%s>", appeal.Status, appeal.TargetDiscordUserID)
		if appeal.ReviewedByDiscordUserID != "" {
			lead += fmt.Sprintf("\nReviewed by <@%s>.", appeal.ReviewedByDiscordUserID)
		}
	case quack.AppealStatusNeedsInformation:
		lead = fmt.Sprintf("Waiting for more information from <@%s>.", appeal.TargetDiscordUserID)
	case quack.AppealStatusClosed:
		lead = fmt.Sprintf("Appeal closed · <@%s>", appeal.TargetDiscordUserID)
	}
	var body []string
	if appeal.DecisionReason != "" {
		body = append(body, PlainText(appeal.DecisionReason))
	}
	if appeal.Statement != "" {
		body = append(body, Quote(PlainText(appeal.Statement)))
	}
	meta := fmt.Sprintf("Case #%d · %s", appeal.CaseNumber, PlainText(appeal.TemplateName))
	message := Conversation("appeal", lead, "", strings.Join(body, "\n\n"), meta, false)
	message.Components = []discordgo.MessageComponent{}
	if appeal.Status == quack.AppealStatusPending {
		message.Components = append(message.Components, Row(
			Button(appealCustomID(appealAcceptAction, appeal.ID), "Accept", discordgo.SuccessButton, false),
			Button(appealCustomID(appealRejectAction, appeal.ID), "Reject", discordgo.DangerButton, false),
		))
	}
	for _, offer := range appeal.ReversalOffers {
		customID, err := EncodeCustomID(CustomID{
			Namespace: appealNamespace,
			Action:    appealReverseAction,
			Version:   "v1",
			Payload:   appeal.ID + "," + offer.OriginalExecutionID + "," + string(offer.ActionType),
		})
		if err != nil {
			continue
		}
		label := "Confirm " + strings.ToLower(offer.ActionType.Label())
		message.Components = append(message.Components, Row(Button(customID, label, discordgo.DangerButton, false)))
	}
	return message
}

// appealDecisionMessage is the DM telling a member what staff decided. It
// quotes the staff reason but never names the reviewer. An accepted appeal
// with a rejoin invite gets a Rejoin Server button.
func appealDecisionMessage(intent quack.AppealDecisionIntent) Message {
	icon, lead, next := "appeal", "Your appeal was closed.", ""
	switch intent.Status {
	case quack.AppealStatusNeedsInformation:
		icon, lead, next = "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."
	case quack.AppealStatusAccepted:
		icon, lead, next = "accept", "Your appeal was accepted.", "Your case was voided. Quack will try to remove any ban or timeout from it."
	case quack.AppealStatusRejected:
		icon, lead = "decline", "Your appeal was rejected."
	}
	var meta []string
	if intent.CaseNumber > 0 {
		meta = append(meta, fmt.Sprintf("Case #%d", intent.CaseNumber))
	} else if intent.CaseID != "" {
		meta = append(meta, "Case "+PlainText(intent.CaseID))
	}
	if intent.GuildName != "" {
		meta = append(meta, PlainText(intent.GuildName))
	}
	body := discordtext.Conversation(icon, lead, PlainText(intent.Reason), next, strings.Join(meta, " · "))
	if intent.RejoinURL != "" {
		body += "\n\nIf you left or were banned, you can rejoin once any ban has been removed: " + intent.RejoinURL
	}
	message := Signal("appeal", body, false)
	if intent.Status == quack.AppealStatusAccepted && intent.RejoinURL != "" {
		message.Components = []discordgo.MessageComponent{Row(LinkButton(intent.RejoinURL, "Rejoin Server"))}
	}
	return message
}

// appealEntryMessage invites the member to appeal from a case notification,
// with a button that opens the case's appeal page in the dashboard. Only an
// https dashboard is accepted, and any query or fragment on it is dropped.
func appealEntryMessage(baseURL, guildID, caseID string) (Message, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return Message{}, errors.New("secure dashboard base URL is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") +
		"/guilds/" + url.PathEscape(guildID) +
		"/cases/" + url.PathEscape(caseID) + "/appeal"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	message := Signal("appeal", "You can ask staff to review this decision.", false)
	message.Components = []discordgo.MessageComponent{Row(LinkButton(parsed.String(), "Appeal decision"))}
	return message, nil
}
