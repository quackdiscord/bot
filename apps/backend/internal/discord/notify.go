package discord

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// The core sends case DMs through DeliverCaseNotification when the
// messenger implements it.
var _ quack.CaseNotificationSender = (*Bot)(nil)

// DeliverCaseNotification DMs the member about their case: the rule, the
// official reason, what happened (with a confirmed timeout's end), and, for
// an appealable case, an "Appeal decision" button. It implements
// quack.CaseNotificationSender. The rendered text comes back even when
// sending fails, so the attempt is kept on the case.
func (b *Bot) DeliverCaseNotification(ctx context.Context, request quack.CaseNotificationRequest) (quack.CaseNotificationReceipt, error) {
	receipt := quack.CaseNotificationReceipt{RenderedMessage: caseNotificationBody(request)}
	if err := ctx.Err(); err != nil {
		return receipt, err
	}
	channelID := strings.TrimSpace(request.PreparedChannelDiscordID)
	if channelID == "" {
		channel, err := b.Session.UserChannelCreate(request.TargetDiscordUserID, rest(ctx)...)
		if err != nil {
			return receipt, classify("dm_prepare", err, false)
		}
		channelID = channel.ID
	}
	notice := Signal("message", receipt.RenderedMessage, false)
	if request.Appealable {
		id, err := EncodeCustomID(CustomID{Namespace: appealNamespace, Action: appealSubmitAction, Version: "v1", Payload: request.CaseID})
		if err != nil {
			return receipt, err
		}
		notice.Components = []discordgo.MessageComponent{Row(Button(id, "Appeal decision", discordgo.PrimaryButton, false))}
	}
	sent, err := b.Send(ctx, channelID, notice)
	if err != nil {
		// A lost response may follow a delivered DM, so a failed send is
		// never safe to repeat.
		return receipt, classify("dm_send", err, true)
	}
	receipt.MessageID = sent.ID
	return receipt, nil
}

// caseNotificationBody renders the member's case DM. It holds only what the
// member may see: no staff context, evidence, or moderator identity.
func caseNotificationBody(request quack.CaseNotificationRequest) string {
	guildName := "this server"
	if strings.TrimSpace(request.GuildName) != "" {
		guildName = request.GuildName
	}
	server := "**" + discordtext.Plain(guildName) + "**"
	icon, lead := "warn", "You received a warning in "+server
	primary, removed := -1, false
outcomes:
	for i, action := range request.Outcomes {
		if action.Status != quack.ActionExecutionSucceeded {
			continue
		}
		switch action.ActionType {
		case quack.ActionTimeoutUser:
			icon, lead = "timeout", "You’ve been timed out in "+server
		case quack.ActionKickUser:
			icon, lead = "kick", "You’ve been removed from "+server
		case quack.ActionBanUser:
			icon, lead = "ban", "You’ve been banned from "+server
		case quack.ActionRemoveTimeout:
			icon, lead, removed = "untimeout", "Your timeout in "+server+" has ended", true
		case quack.ActionUnbanUser:
			icon, lead, removed = "unban", "You’re no longer banned from "+server, true
		default:
			continue
		}
		primary = i
		break outcomes
	}
	var parts []string
	if strings.TrimSpace(request.RuleName) != "" {
		rule := "**" + discordtext.Plain(request.RuleName) + "**"
		if removed {
			parts = append(parts, "This updates your case for "+rule+".")
		} else {
			lead += " for " + rule
		}
	}
	if introduction := strings.TrimSpace(request.Introduction); introduction != "" {
		parts = append(parts, discordtext.Plain(introduction))
	}
	for i, action := range request.Outcomes {
		if action.Status == quack.ActionExecutionFailed && primary == -1 {
			icon = "error"
		}
		if i != primary && action.ActionType != quack.ActionSendDM {
			parts = append(parts, discordtext.ActionSentence(action.ActionType, action.Status))
		}
		if action.ActionType == quack.ActionTimeoutUser && action.Status == quack.ActionExecutionSucceeded && action.TimeoutUntil != nil {
			until := action.TimeoutUntil.Unix()
			parts = append(parts, fmt.Sprintf("You can chat again <t:%d:R> — <t:%d:f>.", until, until))
		}
	}
	if request.Appealable {
		parts = append(parts, "Use the Appeal decision button below to ask the moderators to review this case.")
	}
	if footer := strings.TrimSpace(request.Footer); footer != "" {
		parts = append(parts, discordtext.Plain(footer))
	}
	meta := fmt.Sprintf("Case #%d", request.CaseNumber)
	if !request.CreatedAt.IsZero() {
		meta += fmt.Sprintf(" · <t:%d:R>", request.CreatedAt.Unix())
	}
	return discordtext.Conversation(icon, lead+".", discordtext.Plain(request.Reason), strings.Join(parts, "\n\n"), meta)
}
