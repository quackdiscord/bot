package discord

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// appealStaffMessage is the staff view of an appeal: who appealed, where it
// stands, the member's quoted answers, and a confirmation button for each
// reversal Quack offers after acceptance, handled by appealReversal.
//
// Nothing posts this view yet: staff appeal notifications are plain text, so
// the reversal buttons only appear once the appeal flow sends this message.
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
	for _, answer := range appeal.Answers {
		body = append(body, Quote(PlainText(fmt.Sprint(answer.Value))))
	}
	message := Conversation("appeal", lead, "", strings.Join(body, "\n\n"), "", false)
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
