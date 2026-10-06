package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrCloseNoticeNotSent marks a close DM Discord definitely refused, so a
// later close may try again.
var ErrCloseNoticeNotSent = errors.New("ticket close notice was not sent")

// Close notice states. A notice is pending from just before its send until
// the outcome is saved, so after a crash or lost response Quack looks for
// the DM instead of sending another.
const (
	noticePending  = "pending"
	noticeSent     = "sent"
	noticeRejected = "rejected"
)

// closeNoticeState is the member's close DM, kept under "close_notice" in
// the ticket's metadata.
type closeNoticeState struct {
	State     string `json:"state"`
	MessageID string `json:"message_id,omitempty"`
}

// deliverCloseNotice DMs the member their transcript once. A blocked DM
// never keeps a ticket from closing; an uncertain one is only ever looked
// for, never sent again.
func (a *DiscordAdapter) deliverCloseNotice(ctx context.Context, actor modules.Actor, ticket *Ticket) error {
	store := a.service.store
	before, err := store.updateCloseNotice(ctx, ticket, nil)
	if err != nil {
		return err
	}
	if before.State == noticeSent {
		ticket.CloseNoticeDelivered = true
		return nil
	}
	transcript, err := a.service.Transcript(ctx, actor, ticket.ID)
	if err != nil {
		return err
	}
	messageID, sendErr := a.client.DeliverCloseNotice(ctx, ticket, transcript, before.State == noticePending)
	result := closeNoticeState{State: noticePending}
	switch {
	case sendErr == nil && messageID != "":
		result = closeNoticeState{State: noticeSent, MessageID: messageID}
	case errors.Is(sendErr, ErrCloseNoticeNotSent):
		result.State = noticeRejected
	}
	// Save the outcome even if the request's context ran out.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := store.updateCloseNotice(ctx, ticket, &result); err != nil {
		return err
	}
	ticket.CloseNoticeDelivered = result.State == noticeSent
	return nil
}

// updateCloseNotice returns the saved notice state and, under a row lock,
// either saves result or, with a nil result, marks a new or refused notice
// pending before its send. A sent notice never changes. Other metadata
// keys are kept.
func (s *Store) updateCloseNotice(ctx context.Context, ticket *Ticket, result *closeNoticeState) (closeNoticeState, error) {
	var notice closeNoticeState
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ticketRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND guild_id = ?", ticket.ID, ticket.GuildID).First(&record).Error
		if err != nil {
			return err
		}
		metadata := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(record.MetadataJSON), &metadata); err != nil {
			return err
		}
		if metadata == nil {
			metadata = map[string]json.RawMessage{}
		}
		if raw := metadata["close_notice"]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &notice); err != nil {
				return err
			}
		}
		saved := notice
		switch {
		case result != nil && notice.State != noticeSent:
			saved = *result
		case result == nil && (notice.State == "" || notice.State == noticeRejected):
			saved.State = noticePending
		}
		raw, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		metadata["close_notice"] = raw
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		return tx.Model(&record).Update("metadata_json", string(encoded)).Error
	})
	return notice, err
}
