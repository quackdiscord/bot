package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrAppealDeliveryDeferred means nothing reached Discord, so the
// notification can safely be tried again later. Notifiers wrap it, for
// example when the queue channel is not configured yet.
var ErrAppealDeliveryDeferred = errors.New("appeal delivery deferred")

// ErrAppealNotificationIntent means a stored decision notice is malformed or
// unsupported. It is never sent with substituted wording.
var ErrAppealNotificationIntent = errors.New("appeal notification intent is invalid")

// AppealQueueReceipt locates an appeal's post in the staff queue channel.
// Empty fields mean nothing has been posted yet.
type AppealQueueReceipt struct{ ChannelID, MessageID string }

// AppealDecisionNotice is what a member is told about a decision on their
// appeal, validated before it reaches the notifier.
type AppealDecisionNotice struct {
	Intent AppealDecisionIntent
	// GuildID is Quack's ID for the appeal's guild, which with
	// Intent.CaseID locates the member's appeal page in the dashboard.
	GuildID string
}

// AppealDecisionSender is implemented by notifiers that render a member's
// decision notice themselves. When the dispatcher's notifier implements it,
// decision notices go through it instead of the plain Body.
type AppealDecisionSender interface {
	// SendAppealDecision DMs the member and returns the message ID.
	SendAppealDecision(ctx context.Context, discordUserID string, notice AppealDecisionNotice) (string, error)
}

// AppealQueuePublisher is implemented by notifiers that keep one staff queue
// post per appeal. When the dispatcher's notifier implements it, staff
// notifications publish the whole appeal instead of the plain Body.
type AppealQueuePublisher interface {
	// PublishAppealQueue posts the appeal to the guild's queue channel, or
	// edits the post at receipt when it is still there, and returns where
	// the appeal is now shown. guildID is Quack's guild ID.
	PublishAppealQueue(ctx context.Context, guildID string, appeal *AppealResponse, receipt AppealQueueReceipt) (AppealQueueReceipt, error)
}

// AppealNotificationDispatcher delivers the appeal notification outbox.
// Rows are written in the same transaction as the appeal change, so a
// notification is never lost even if Discord is down when it happens.
type AppealNotificationDispatcher struct {
	store   AppealNotificationStore
	client  AppealNotifier
	appeals *AppealService
}

// NewAppealNotificationDispatcher returns a dispatcher that sends through
// client, using its AppealDecisionSender and AppealQueuePublisher methods
// when it has them.
func NewAppealNotificationDispatcher(store AppealNotificationStore, client AppealNotifier) *AppealNotificationDispatcher {
	return &AppealNotificationDispatcher{store: store, client: client, appeals: NewAppealService(store)}
}

// DispatchPending sends up to limit pending notifications and records each
// outcome. limit must be between 1 and 100.
func (d *AppealNotificationDispatcher) DispatchPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return errors.New("appeal notification limit is invalid")
	}
	items, err := d.store.ClaimPendingAppealNotifications(ctx, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := d.store.BeginAppealNotificationDelivery(ctx, item.ID, item.LeaseToken); err != nil {
			return err
		}
		receipt, sendErr := d.deliver(ctx, item)
		params := CompleteAppealNotificationParams{
			NotificationID:    item.ID,
			LeaseToken:        item.LeaseToken,
			DeliveryChannelID: receipt.ChannelID,
			DeliveryMessageID: receipt.MessageID,
			Status:            AppealNotificationSent,
		}
		if sendErr != nil {
			params.Status = AppealNotificationFailed
			params.ErrorCode = appealNotificationErrorCode(sendErr)
		}
		if err := d.store.CompleteAppealNotification(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

// deliver sends one notification and returns where it landed.
func (d *AppealNotificationDispatcher) deliver(ctx context.Context, item AppealNotification) (AppealQueueReceipt, error) {
	switch item.Audience {
	case AppealNotificationMember:
		var messageID string
		var err error
		if sender, ok := d.client.(AppealDecisionSender); ok && item.DecisionIntentJSON != "" {
			var notice AppealDecisionNotice
			if notice, err = decodeDecisionNotice(item.DecisionIntentJSON); err == nil {
				notice.GuildID = item.GuildID
				messageID, err = sender.SendAppealDecision(ctx, item.TargetDiscordUserID, notice)
			}
		} else {
			messageID, err = d.client.SendAppealMemberNotification(ctx, item.TargetDiscordUserID, item.Body)
		}
		return AppealQueueReceipt{MessageID: messageID}, err
	case AppealNotificationStaff:
		publisher, ok := d.client.(AppealQueuePublisher)
		if !ok {
			messageID, err := d.client.SendAppealStaffNotification(ctx, item.GuildID, item.Body)
			return AppealQueueReceipt{MessageID: messageID}, err
		}
		receipt := AppealQueueReceipt{ChannelID: item.DeliveryChannelID, MessageID: item.DeliveryMessageID}
		appeal, err := d.store.GetAppealByID(ctx, item.AppealID)
		if err != nil {
			return receipt, fmt.Errorf("%w: %v", ErrAppealDeliveryDeferred, err)
		}
		if appeal == nil || appeal.GuildID != item.GuildID {
			return receipt, ErrAppealNotFound
		}
		response, err := d.appeals.response(ctx, appeal, false)
		if err != nil {
			return receipt, fmt.Errorf("%w: %v", ErrAppealDeliveryDeferred, err)
		}
		return publisher.PublishAppealQueue(ctx, item.GuildID, response, receipt)
	default:
		return AppealQueueReceipt{}, errors.New("appeal notification audience is invalid")
	}
}

// decodeDecisionNotice validates a stored decision intent. A malformed one
// is an error rather than a reason to improvise wording.
func decodeDecisionNotice(body string) (AppealDecisionNotice, error) {
	var intent AppealDecisionIntent
	if json.Unmarshal([]byte(body), &intent) != nil || intent.Version != 1 ||
		!utf8.ValidString(intent.Reason) || strings.TrimSpace(intent.Reason) == "" || len([]rune(intent.Reason)) > 2000 {
		return AppealDecisionNotice{}, ErrAppealNotificationIntent
	}
	switch intent.Status {
	case AppealStatusAccepted, AppealStatusRejected, AppealStatusNeedsInformation, AppealStatusClosed:
	default:
		return AppealDecisionNotice{}, ErrAppealNotificationIntent
	}
	if intent.RejoinURL != "" {
		normalized, err := normalizeRejoinURL(intent.RejoinURL)
		if intent.Status != AppealStatusAccepted || err != nil || normalized != intent.RejoinURL {
			return AppealDecisionNotice{}, ErrAppealNotificationIntent
		}
	}
	return AppealDecisionNotice{Intent: intent}, nil
}

// appealNotificationErrorCode reduces a delivery error to a coarse code so
// no Discord response text is stored. It reads the adapter's DiscordError
// classification, never the error text. Deferred deliveries are retried.
func appealNotificationErrorCode(err error) string {
	var discordErr DiscordError
	switch {
	case errors.Is(err, ErrAppealDeliveryDeferred):
		return AppealDeliveryDeferredCode
	case errors.Is(err, ErrAppealNotificationIntent):
		return "invalid_notification_intent"
	case errors.Is(err, context.DeadlineExceeded):
		return "discord_timeout"
	case !errors.As(err, &discordErr):
		return "discord_delivery_failed"
	case discordErr.HasFailure(DiscordFailurePermissionDenied):
		return "discord_forbidden"
	case discordErr.HasFailure(DiscordFailureRateLimited):
		return "discord_rate_limited"
	default:
		return "discord_delivery_failed"
	}
}

// AppealDeliveryDeferredCode is the error code of a failed appeal
// notification that nothing reached Discord for. The store claims such rows
// again after a minute.
const AppealDeliveryDeferredCode = "delivery_deferred"
