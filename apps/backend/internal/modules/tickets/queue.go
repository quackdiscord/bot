package tickets

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
)

// Queue post delivery errors. A send whose outcome is unknown is never
// repeated automatically, since it may already be in the channel.
var (
	// ErrQueueNotSent marks a queue send Discord definitely refused, so
	// another send is safe.
	ErrQueueNotSent = errors.New("ticket queue message was not sent")
	// ErrQueueDeliveryUnknown reports a queue send without a confirmed
	// receipt. An administrator must check the queue channel and reconcile
	// it before Quack sends again.
	ErrQueueDeliveryUnknown = errors.New("ticket queue delivery is unconfirmed; check the staff queue before retrying")
	// ErrQueueMessageMissing reports that a saved queue post is gone.
	ErrQueueMessageMissing = errors.New("ticket queue message is missing")
)

// publishQueue posts or edits the ticket's queue post and saves the
// receipt; with a transcript, the receipt's link is what later allows the
// thread to be deleted. A first send is fenced in the database before it
// leaves, and only a definite refusal lifts the fence.
func (a *DiscordAdapter) publishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) (string, error) {
	store := a.service.store
	if settings.QueueChannelDiscordID == "" {
		return "", errors.New("ticket queue channel is not configured")
	}
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID != settings.QueueChannelDiscordID {
		// The queue moved: retire the old post's receipt and send anew.
		if err := store.clearQueueReceipt(ctx, ticket); err != nil {
			return "", err
		}
	}
	firstSend := ticket.LogMessageDiscordID == ""
	if firstSend {
		if err := store.reserveQueueSend(ctx, ticket, settings.QueueChannelDiscordID); err != nil {
			return "", err
		}
	}
	receipt, err := a.client.PublishQueue(ctx, ticket, settings, transcript)
	if !firstSend && errors.Is(err, ErrQueueMessageMissing) {
		if err := store.clearQueueReceipt(ctx, ticket); err != nil {
			return "", err
		}
		return a.publishQueue(ctx, ticket, settings, transcript)
	}
	if err != nil {
		if firstSend && errors.Is(err, ErrQueueNotSent) {
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			err = errors.Join(err, store.releaseQueueSend(ctx, ticket, settings.QueueChannelDiscordID))
		}
		return "", err
	}
	if receipt == nil || receipt.MessageID == "" {
		return "", errors.New("ticket queue delivery returned no message")
	}
	url := ""
	if transcript != nil {
		if url = receipt.URL; url == "" {
			return "", errors.New("ticket transcript delivery returned no link")
		}
	}
	if err := store.saveQueueReceipt(ctx, ticket, settings.QueueChannelDiscordID, receipt.MessageID, url); err != nil {
		return "", err
	}
	return receipt.MessageID, nil
}

// checkQueueReceipt confirms a saved queue post still exists before it is
// trusted. Only Discord saying it is gone clears the receipt; a failed read
// keeps it and stops the caller.
func (a *DiscordAdapter) checkQueueReceipt(ctx context.Context, ticket *Ticket) error {
	if ticket.LogMessageDiscordID == "" {
		return nil
	}
	exists, err := a.client.QueueMessageExists(ctx, ticket.LogChannelDiscordID, ticket.LogMessageDiscordID)
	if err != nil {
		return err
	}
	if !exists {
		return a.service.store.clearQueueReceipt(ctx, ticket)
	}
	return nil
}

// reserveQueueSend fences a first queue send: the destination is recorded
// with a fresh attempt ID and no message. The conditional update also
// keeps two repairs from sending at once.
func (s *Store) reserveQueueSend(ctx context.Context, ticket *Ticket, channelID string) error {
	attemptID := ulid.Make().String()
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID).
		Where("COALESCE(log_channel_discord_id, '') = '' AND COALESCE(log_message_discord_id, '') = '' AND COALESCE(queue_delivery_attempt_id, '') = ''").
		Updates(map[string]any{"log_channel_discord_id": channelID, "queue_delivery_attempt_id": attemptID})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID, ticket.QueueDeliveryAttemptID = channelID, attemptID
	return nil
}

// releaseQueueSend lifts the fence after Discord definitely refused the
// send. If this write fails, the fence stays, which is the safe side.
func (s *Store) releaseQueueSend(ctx context.Context, ticket *Ticket, channelID string) error {
	if ticket.QueueDeliveryAttemptID == "" {
		return ErrQueueDeliveryUnknown
	}
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("id = ? AND guild_id = ? AND log_channel_discord_id = ? AND queue_delivery_attempt_id = ?",
			ticket.ID, ticket.GuildID, channelID, ticket.QueueDeliveryAttemptID).
		Where("COALESCE(log_message_discord_id, '') = ''").
		Updates(map[string]any{"log_channel_discord_id": "", "queue_delivery_attempt_id": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID, ticket.QueueDeliveryAttemptID = "", ""
	return nil
}

// saveQueueReceipt replaces the send fence, or the previous receipt, with
// the confirmed post. transcriptURL is empty for an open ticket's post.
func (s *Store) saveQueueReceipt(ctx context.Context, ticket *Ticket, channelID, messageID, transcriptURL string) error {
	query := s.db.WithContext(ctx).Model(&ticketRecord{}).Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID)
	if ticket.QueueDeliveryAttemptID != "" {
		query = query.Where("queue_delivery_attempt_id = ? AND COALESCE(log_message_discord_id, '') = ''", ticket.QueueDeliveryAttemptID)
	} else {
		query = query.Where("COALESCE(queue_delivery_attempt_id, '') = '' AND log_channel_discord_id = ? AND log_message_discord_id = ?",
			ticket.LogChannelDiscordID, ticket.LogMessageDiscordID)
	}
	result := query.Updates(map[string]any{
		"log_channel_discord_id":    channelID,
		"log_message_discord_id":    messageID,
		"transcript_url":            transcriptURL,
		"queue_delivery_attempt_id": "",
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID, ticket.LogMessageDiscordID, ticket.TranscriptURL = channelID, messageID, transcriptURL
	ticket.QueueDeliveryAttemptID = ""
	return nil
}

// clearQueueReceipt retires a post that is gone or in an old queue channel.
// Its transcript link goes too, so the thread is not deleted on the
// strength of a post nobody can see.
func (s *Store) clearQueueReceipt(ctx context.Context, ticket *Ticket) error {
	result := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("id = ? AND guild_id = ? AND log_channel_discord_id = ? AND log_message_discord_id = ?",
			ticket.ID, ticket.GuildID, ticket.LogChannelDiscordID, ticket.LogMessageDiscordID).
		Where("COALESCE(queue_delivery_attempt_id, '') = ''").
		Updates(map[string]any{"log_channel_discord_id": "", "log_message_discord_id": "", "transcript_url": ""})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrQueueDeliveryUnknown
	}
	ticket.LogChannelDiscordID, ticket.LogMessageDiscordID, ticket.TranscriptURL = "", "", ""
	return nil
}
