package discord

import (
	"errors"
	"fmt"
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
// appealURL is the appeal's staff dashboard page, or "" to leave it out.
func appealStaffPage(appeal *quack.AppealResponse, page int, applicationID, appealURL string) Message {
	message := appealStaffMessage(appeal, appealURL)
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
// Reject while it is pending, a link to appealURL (its staff dashboard
// page, when set), and a confirmation button for each reversal still on
// offer after acceptance.
func appealStaffMessage(appeal *quack.AppealResponse, appealURL string) Message {
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
		message.Components = append(message.Components, Row(appendLink([]discordgo.MessageComponent{
			Button(appealCustomID(appealAcceptAction, appeal.ID), "Accept", discordgo.SuccessButton, false),
			Button(appealCustomID(appealRejectAction, appeal.ID), "Reject", discordgo.DangerButton, false),
		}, appealURL, dashboardLabel)...))
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
	if appeal.Status != quack.AppealStatusPending {
		message = withLink(message, appealURL, dashboardLabel)
	}
	return message
}

// appealDecisionMessage is the DM telling a member what staff decided. It
// quotes the staff reason but never names the reviewer. An accepted appeal
// with a rejoin invite gets a Rejoin Server button. appealURL, when set, is
// the member's appeal page in the dashboard: a request for information
// links it as the place to reply, and any other decision as the appeal.
func appealDecisionMessage(intent quack.AppealDecisionIntent, appealURL string) Message {
	icon, lead, next := "appeal", "Your appeal was closed.", ""
	label := dashboardAppealLabel
	switch intent.Status {
	case quack.AppealStatusNeedsInformation:
		icon, lead, next = "reply", "Staff need a little more information to review your appeal.", "You can reply from your Quack dashboard."
		label = dashboardReplyLabel
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
	var buttons []discordgo.MessageComponent
	if intent.Status == quack.AppealStatusAccepted && intent.RejoinURL != "" {
		buttons = append(buttons, LinkButton(intent.RejoinURL, "Rejoin Server"))
	}
	if buttons = appendLink(buttons, appealURL, label); len(buttons) > 0 {
		message.Components = []discordgo.MessageComponent{Row(buttons...)}
	}
	return message
}

// appealEntryMessage invites the member to appeal from a case notification,
// with a button that opens the case's appeal page in the dashboard at
// baseURL. The base comes from quack.ActionService, whose DashboardLinks
// already applied the environment's scheme rule; one that is not a plain
// http(s) URL, or IDs that are not safe path segments, are an error.
func appealEntryMessage(baseURL, guildID, caseID string) (Message, error) {
	link := quack.NewDashboardLinks(baseURL).MemberAppeal(guildID, caseID)
	if link == "" {
		return Message{}, errors.New("dashboard appeal link is unavailable")
	}
	message := Signal("appeal", "You can ask staff to review this decision.", false)
	message.Components = []discordgo.MessageComponent{Row(LinkButton(link, "Appeal decision"))}
	return message, nil
}
