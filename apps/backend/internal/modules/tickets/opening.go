package tickets

import (
	"context"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
)

// ticketOpeningTTL bounds a provisional member reservation after a worker stops.
// Provisioning has a shorter deadline; its fencing token cannot complete after a
// replacement reservation has acquired the member slot.
const ticketOpeningTTL = 5 * time.Minute

// reserveOpening consumes the member's duplicate and daily allowance before any
// Discord channel is created. Failed attempts retain their daily count so a
// member cannot repeatedly force expensive provisioning failures.
func (s *Store) reserveOpening(ctx context.Context, actor modules.Actor, dailyLimit int, now time.Time) (string, error) {
	if s == nil || s.db == nil || actor.GuildID == "" || actor.DiscordUserID == "" {
		return "", errors.New("ticket member and database are required")
	}
	token := ulid.Make().String()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockMemberState(tx, actor.GuildID, actor.DiscordUserID, now)
		if err != nil {
			return err
		}
		if state.OpenTicketID != "" {
			var count int64
			if err := tx.Model(&ticketRecord{}).Where("id = ?", state.OpenTicketID).Count(&count).Error; err != nil {
				return err
			}
			if count != 0 || now.Sub(state.UpdatedAt) < ticketOpeningTTL {
				return ErrDuplicateOpen
			}
		}
		if now.Sub(state.WindowStartedAt) >= 24*time.Hour {
			state.WindowStartedAt, state.OpenCount = now, 0
		}
		if state.OpenCount >= dailyLimit {
			return ErrRateLimited
		}
		state.OpenTicketID, state.UpdatedAt = token, now
		state.OpenCount++
		return tx.Model(state).Updates(map[string]any{
			"open_ticket_id": state.OpenTicketID, "open_count": state.OpenCount,
			"window_started_at": state.WindowStartedAt, "updated_at": now,
		}).Error
	})
	return token, err
}

// finishOpening converts only the current member reservation into a durable
// ticket and timeline. It never consumes a second daily allowance.
func (s *Store) finishOpening(ctx context.Context, actor modules.Actor, token, channelID string, now time.Time) (*Ticket, error) {
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockMemberState(tx, actor.GuildID, actor.DiscordUserID, now)
		if err != nil {
			return err
		}
		if state.OpenTicketID != token || now.Sub(state.UpdatedAt) >= ticketOpeningTTL {
			return ErrInvalidTransition
		}
		record := ticketRecord{ID: token, GuildID: actor.GuildID, OwnerDiscordUserID: actor.DiscordUserID,
			ThreadDiscordChannelID: channelID, Status: StatusOpen, MetadataJSON: "{}", CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		if err := appendEvent(tx, record, EventOpened, actor.DiscordUserID, "Ticket opened", "{}", now); err != nil {
			return err
		}
		ticket = ticketFromRecord(record)
		return nil
	})
	return &ticket, err
}

// releaseOpening clears a failed provision only while its original token owns
// the member slot. Successful tickets and newer reservations are unaffected.
func (s *Store) releaseOpening(ctx context.Context, actor modules.Actor, token string) error {
	return s.db.WithContext(ctx).Model(&memberStateRecord{}).
		Where("guild_id = ? AND owner_discord_user_id = ? AND open_ticket_id = ?", actor.GuildID, actor.DiscordUserID, token).
		Where("NOT EXISTS (SELECT 1 FROM tickets WHERE tickets.id = ?)", token).
		Update("open_ticket_id", "").Error
}
