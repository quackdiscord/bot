package tickets

import (
	"context"
	"errors"
	"log/slog"
)

// ErrQueueMessageMissing reports that a saved queue post is gone.
var ErrQueueMessageMissing = errors.New("ticket queue message is missing")

// publishQueue posts the ticket to the staff queue, or edits its known post,
// and saves the receipt; with a transcript the post shows the ticket closed.
// The post is a convenience for staff, since the thread is the ticket, so it
// is best-effort: a failure is logged and never stops opening or closing. A
// known post that Discord says is gone is replaced with a fresh one.
func (a *DiscordAdapter) publishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) {
	err := a.sendQueuePost(ctx, ticket, settings, transcript)
	if err != nil {
		slog.WarnContext(ctx, "Ticket queue post failed", "guild_id", ticket.GuildID, "ticket_id", ticket.ID,
			"closed", transcript != nil, "error", err)
	}
}

// sendQueuePost does publishQueue's work and reports what went wrong.
func (a *DiscordAdapter) sendQueuePost(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) error {
	if settings.QueueChannelDiscordID == "" {
		return errors.New("ticket queue channel is not configured")
	}
	receipt, err := a.client.PublishQueue(ctx, ticket, settings, transcript)
	if errors.Is(err, ErrQueueMessageMissing) {
		fresh := *ticket
		fresh.LogMessageDiscordID = ""
		receipt, err = a.client.PublishQueue(ctx, &fresh, settings, transcript)
	}
	if err != nil {
		return err
	}
	if receipt == nil || receipt.MessageID == "" {
		return errors.New("ticket queue delivery returned no message")
	}
	url := ""
	if transcript != nil {
		if url = receipt.URL; url == "" {
			return errors.New("ticket transcript delivery returned no link")
		}
	}
	return a.service.store.saveQueueReceipt(ctx, ticket, settings.QueueChannelDiscordID, receipt.MessageID, url)
}

// saveQueueReceipt records the ticket's queue post. transcriptURL is empty
// for an open ticket's post.
func (s *Store) saveQueueReceipt(ctx context.Context, ticket *Ticket, channelID, messageID, transcriptURL string) error {
	err := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID).
		Updates(map[string]any{
			"log_channel_discord_id": channelID,
			"log_message_discord_id": messageID,
			"transcript_url":         transcriptURL,
		}).Error
	if err != nil {
		return err
	}
	ticket.LogChannelDiscordID, ticket.LogMessageDiscordID, ticket.TranscriptURL = channelID, messageID, transcriptURL
	return nil
}
