package discord

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// AppealSettingsStore is the storage the AppealNotifier reads to find a
// guild's appeal queue channel.
type AppealSettingsStore interface {
	GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error)
	GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error)
}

// AppealNotifier delivers the appeal outbox through Discord. It implements
// quack.AppealNotifier, quack.AppealDecisionSender, and
// quack.AppealQueuePublisher: members get decision DMs, and staff get one
// post per appeal in the appeal queue channel, edited as the appeal
// changes. Messages never name the staff member involved.
type AppealNotifier struct {
	bot      *Bot
	channels appealChannels
}

// NewAppealNotifier returns an AppealNotifier that sends through bot. Staff
// posts go to the guild's appeal queue channel, found in store.
func NewAppealNotifier(bot *Bot, store AppealSettingsStore) *AppealNotifier {
	return &AppealNotifier{bot: bot, channels: appealChannels{store: store, validator: bot}}
}

// staffChannelValidator is the staff-only check appealChannels runs before
// every send; tests replace the live Discord check.
type staffChannelValidator interface {
	ValidateStaffChannel(ctx context.Context, guildID, channelID string) error
}

// appealChannels finds a guild's appeal queue channel, re-checked as
// staff-only before every send, so a channel that has since become public
// is never used.
type appealChannels struct {
	store     AppealSettingsStore
	validator staffChannelValidator
}

// queueChannel returns the guild's appeal queue channel. A guild without
// one, or whose channel fails the staff-only check, gets an error wrapping
// quack.ErrAppealDeliveryDeferred, since nothing was sent and setting the
// channel up later should still deliver the appeal.
func (c appealChannels) queueChannel(ctx context.Context, guildID string) (string, error) {
	settings, err := c.store.GetGuildSettings(ctx, guildID)
	if err != nil {
		return "", fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	if settings == nil || strings.TrimSpace(settings.AppealQueueChannelDiscordID) == "" {
		return "", fmt.Errorf("%w: appeal queue channel is not configured", quack.ErrAppealDeliveryDeferred)
	}
	guild, err := c.store.GetGuildByID(ctx, guildID)
	if err != nil || guild == nil {
		return "", fmt.Errorf("%w: appeal guild is unavailable", quack.ErrAppealDeliveryDeferred)
	}
	channelID := settings.AppealQueueChannelDiscordID
	if err := c.validator.ValidateStaffChannel(ctx, guild.DiscordGuildID, channelID); err != nil {
		return "", fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, err)
	}
	return channelID, nil
}

// SendAppealMemberNotification sends a plain status update to the member by
// DM. Decision notices go through SendAppealDecision instead.
func (n *AppealNotifier) SendAppealMemberNotification(ctx context.Context, discordUserID, body string) (string, error) {
	return n.dm(ctx, discordUserID, Signal("appeal", body, false))
}

// SendAppealDecision DMs the member the decision on their appeal, with a
// Rejoin Server button when an accepted appeal carries an invite.
func (n *AppealNotifier) SendAppealDecision(ctx context.Context, discordUserID string, notice quack.AppealDecisionNotice) (string, error) {
	return n.dm(ctx, discordUserID, appealDecisionMessage(notice.Intent))
}

// dm sends message to the member. A rate limit is deferred, since nothing
// was sent; a closed or blocked DM is a recorded failure rather than
// something to probe again and again.
func (n *AppealNotifier) dm(ctx context.Context, discordUserID string, message Message) (string, error) {
	if strings.TrimSpace(discordUserID) == "" {
		return "", errors.New("appeal member is unknown")
	}
	channel, err := n.bot.Session.UserChannelCreate(discordUserID, rest(ctx)...)
	if err != nil {
		return "", memberSendError("appeal_dm_channel", err)
	}
	sent, err := n.bot.Send(ctx, channel.ID, message)
	if err != nil {
		return "", memberSendError("appeal_dm", err)
	}
	return sent.ID, nil
}

// SendAppealStaffNotification posts a plain update to the guild's appeal
// queue channel. Staff updates normally go through PublishAppealQueue.
func (n *AppealNotifier) SendAppealStaffNotification(ctx context.Context, guildID, body string) (string, error) {
	channelID, err := n.channels.queueChannel(ctx, guildID)
	if err != nil {
		return "", err
	}
	sent, err := n.bot.Send(ctx, channelID, Signal("appeal", body, false))
	if err != nil {
		return "", queueSendError(err)
	}
	return sent.ID, nil
}

// PublishAppealQueue shows appeal in the guild's appeal queue channel. A
// post still in that channel is edited in place; otherwise, or when the
// post was deleted, a new one is posted and its receipt returned. Editing
// is idempotent, so any failed edit is retried, but a failed post is only
// retried when Discord clearly refused it.
func (n *AppealNotifier) PublishAppealQueue(
	ctx context.Context, guildID string, appeal *quack.AppealResponse, receipt quack.AppealQueueReceipt,
) (quack.AppealQueueReceipt, error) {
	channelID, err := n.channels.queueChannel(ctx, guildID)
	if err != nil {
		return receipt, err
	}
	applicationID := n.bot.applicationID(ctx)
	message := appealStaffPage(appeal, 1, applicationID)
	if receipt.ChannelID == channelID && receipt.MessageID != "" {
		edit := EditMessage(message).ForApplication(applicationID).webhookEdit()
		_, err := n.bot.Session.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:              receipt.MessageID,
			Channel:         channelID,
			Content:         edit.Content,
			Components:      edit.Components,
			Embeds:          edit.Embeds,
			Attachments:     edit.Attachments,
			Files:           edit.Files,
			AllowedMentions: edit.AllowedMentions,
		}, rest(ctx)...)
		if err == nil {
			return receipt, nil
		}
		var restErr *discordgo.RESTError
		if !errors.As(err, &restErr) || restErr.Message == nil || restErr.Message.Code != discordgo.ErrCodeUnknownMessage {
			return receipt, fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, classify("appeal_queue_edit", err, false))
		}
	}
	sent, err := n.bot.Send(ctx, channelID, message)
	if err != nil {
		return receipt, queueSendError(err)
	}
	return quack.AppealQueueReceipt{ChannelID: channelID, MessageID: sent.ID}, nil
}

// queueSendError defers only posts Discord clearly refused (missing access,
// unknown channel, rate limit). A network or server error may follow a post
// that went through, so it is a failure instead of a duplicate post.
func queueSendError(err error) error {
	switch status := statusCode(err); {
	case rateLimited(err), status == http.StatusForbidden, status == http.StatusNotFound:
		return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, classify("appeal_queue_post", err, true))
	}
	return notificationError("appeal_queue_post", err)
}

// memberSendError defers a rate-limited DM and classifies anything else.
func memberSendError(operation string, err error) error {
	if rateLimited(err) {
		return fmt.Errorf("%w: %v", quack.ErrAppealDeliveryDeferred, classify(operation, err, false))
	}
	return notificationError(operation, err)
}

// notificationError classifies a failed appeal notification send, so the
// dispatcher can record a coarse code without reading Discord's text. A
// deadline is kept as is, so it still reads as a timeout.
func notificationError(operation string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, classify(operation, err, false))
}

// rateLimited reports whether Discord refused a request for its rate limit,
// either as a 429 response or discordgo's own rate limit error.
func rateLimited(err error) bool {
	var rateLimit *discordgo.RateLimitError
	return errors.As(err, &rateLimit) || statusCode(err) == http.StatusTooManyRequests
}
