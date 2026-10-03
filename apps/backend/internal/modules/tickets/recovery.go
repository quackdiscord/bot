package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrInvalidQueueReceipt rejects a recovery decision that is incomplete, or
// a message link that is not Quack's queue post for the ticket.
var ErrInvalidQueueReceipt = errors.New("ticket queue recovery requires a verified message or explicit nondelivery confirmation")

// QueueRecovery is a queue send whose outcome is unknown, for an
// administrator to check. AttemptID must come back unchanged with the
// decision, so a decision about one send cannot clear a later one to the
// same channel.
type QueueRecovery struct {
	TicketID         string
	ChannelDiscordID string
	AttemptID        string
}

// QueueRecoveryInput is an administrator's decision about one uncertain
// send: either the link of the post that did arrive, or confirmation that
// none did, which lets the next repair or close send again.
type QueueRecoveryInput struct {
	TicketID            string
	AttemptID           string
	MessageURL          string
	ConfirmNotDelivered bool
}

// QueueRecovery returns a ticket's uncertain queue send to a manager. A
// fence from before attempt IDs gets one, without being lifted.
func (a *DiscordAdapter) QueueRecovery(ctx context.Context, actor modules.Actor, ticketID string) (*QueueRecovery, error) {
	if !actor.CanManage {
		return nil, ErrPermissionDenied
	}
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+ticketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, err := a.service.store.queueRecoveryTicket(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	return &QueueRecovery{
		TicketID:         ticket.ID,
		ChannelDiscordID: ticket.LogChannelDiscordID,
		AttemptID:        ticket.QueueDeliveryAttemptID,
	}, nil
}

// ReconcileQueue records a manager's decision about one uncertain send. It
// never sends, deletes the thread, or frees the member's slot. Adopting a
// post leaves the transcript link empty, so closing still attaches the
// saved transcript before the thread is deleted.
func (a *DiscordAdapter) ReconcileQueue(ctx context.Context, actor modules.Actor, input QueueRecoveryInput) (*Ticket, error) {
	if !actor.CanManage {
		return nil, ErrPermissionDenied
	}
	input.MessageURL = strings.TrimSpace(input.MessageURL)
	if input.TicketID == "" || input.AttemptID == "" || (input.MessageURL != "") == input.ConfirmNotDelivered {
		return nil, ErrInvalidQueueReceipt
	}
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+input.TicketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, err := a.service.store.queueRecoveryTicket(ctx, actor.GuildID, input.TicketID)
	if err != nil {
		return nil, err
	}
	if ticket.QueueDeliveryAttemptID != input.AttemptID {
		return nil, ErrQueueDeliveryUnknown
	}
	var receipt *QueueReceipt
	if input.MessageURL != "" {
		receipt, err = a.client.ValidateQueueMessage(ctx, ticket, input.MessageURL)
		if err != nil {
			return nil, err
		}
		if receipt == nil || receipt.MessageID == "" {
			return nil, ErrInvalidQueueReceipt
		}
	}
	ticket, err = a.service.store.reconcileQueue(ctx, actor, input, receipt, a.service.now())
	if err != nil {
		a.service.audit(ctx, actor, "ticket.queue.reconciled", input.TicketID, "failure", err)
		return nil, err
	}
	a.service.audit(ctx, actor, "ticket.queue.reconciled", input.TicketID, "success", nil)
	return ticket, nil
}

// queueRecoveryTicket returns a ticket with an uncertain send, giving an
// old fence an attempt ID under the row lock so repeated inspection sees a
// stable one.
func (s *Store) queueRecoveryTicket(ctx context.Context, guildID, ticketID string) (*Ticket, error) {
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockRecoverableTicket(tx, guildID, ticketID)
		if err != nil {
			return err
		}
		if record.LogChannelDiscordID == "" || record.LogMessageDiscordID != "" {
			return ErrInvalidTransition
		}
		if record.QueueDeliveryAttemptID == "" {
			record.QueueDeliveryAttemptID = ulid.Make().String()
			if err := tx.Model(record).Update("queue_delivery_attempt_id", record.QueueDeliveryAttemptID).Error; err != nil {
				return err
			}
		}
		ticket = ticketFromRecord(*record)
		return nil
	})
	return &ticket, err
}

// reconcileQueue saves one decision for the matching attempt, with its
// timeline entry, in one transaction.
func (s *Store) reconcileQueue(ctx context.Context, actor modules.Actor, input QueueRecoveryInput, receipt *QueueReceipt, now time.Time) (*Ticket, error) {
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := lockRecoverableTicket(tx, actor.GuildID, input.TicketID)
		if err != nil {
			return err
		}
		if record.QueueDeliveryAttemptID != input.AttemptID || record.LogChannelDiscordID == "" || record.LogMessageDiscordID != "" {
			return ErrQueueDeliveryUnknown
		}
		channelID, messageID, decision := "", "", "confirmed_not_delivered"
		if receipt != nil {
			channelID, messageID, decision = record.LogChannelDiscordID, receipt.MessageID, "adopted_existing_message"
		}
		result := tx.Model(&ticketRecord{}).
			Where("id = ? AND guild_id = ? AND queue_delivery_attempt_id = ?", record.ID, record.GuildID, input.AttemptID).
			Updates(map[string]any{
				"queue_delivery_attempt_id": "",
				"log_channel_discord_id":    channelID,
				"log_message_discord_id":    messageID,
				"transcript_url":            "",
				"updated_at":                now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrQueueDeliveryUnknown
		}
		metadata, err := json.Marshal(map[string]string{
			"decision":   decision,
			"attempt_id": input.AttemptID,
			"channel_id": record.LogChannelDiscordID,
			"message_id": messageID,
		})
		if err != nil {
			return err
		}
		err = appendEvent(tx, *record, EventQueueReconciled, actor.DiscordUserID,
			"Staff queue delivery reconciled", string(metadata), now)
		if err != nil {
			return err
		}
		record.QueueDeliveryAttemptID, record.TranscriptURL = "", ""
		record.LogChannelDiscordID, record.LogMessageDiscordID = channelID, messageID
		record.UpdatedAt = now
		ticket = ticketFromRecord(*record)
		return nil
	})
	return &ticket, err
}

// lockRecoverableTicket locks a ticket that is open or closing and still
// holds its owner's slot; a finished or superseded ticket is not
// recoverable.
func lockRecoverableTicket(tx *gorm.DB, guildID, ticketID string) (*ticketRecord, error) {
	var record ticketRecord
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("guild_id = ? AND id = ?", guildID, ticketID).First(&record).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if record.Status != StatusOpen && record.Status != StatusResolved {
		return nil, ErrInvalidTransition
	}
	var state memberStateRecord
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("guild_id = ? AND owner_discord_user_id = ?", guildID, record.OwnerDiscordUserID).
		Limit(1).Find(&state).Error
	if err != nil {
		return nil, err
	}
	if state.OpenTicketID != ticketID {
		return nil, ErrInvalidTransition
	}
	return &record, nil
}
