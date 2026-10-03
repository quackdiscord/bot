package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// openingTTL bounds a member's opening reservation, so a worker that dies
// mid-provisioning cannot block the member forever. A reservation older than
// this can be replaced, and its token can no longer finish the opening.
const openingTTL = 5 * time.Minute

// ticketRecord is a row of tickets.
type ticketRecord struct {
	ID                      string     `gorm:"type:char(26);primaryKey"`
	GuildID                 string     `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string     `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string     `gorm:"size:32;uniqueIndex"`
	Status                  Status     `gorm:"size:32;not null;index:idx_ticket_guild_status,priority:2"`
	LogChannelDiscordID     string     `gorm:"size:32"`
	LogMessageDiscordID     string     `gorm:"size:32"`
	TranscriptURL           string     `gorm:"size:1024"`
	ResolvedByDiscordUserID string     `gorm:"size:32"`
	ResolvedAt              *time.Time `gorm:"index"`
	// MetadataJSON holds the close notice state; see closeNoticeState.
	MetadataJSON         string `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt time.Time
}

func (ticketRecord) TableName() string { return "tickets" }

// eventRecord is a row of ticket_events.
type eventRecord struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	TicketID             string    `gorm:"type:char(26);not null;index"`
	GuildID              string    `gorm:"type:char(26);not null;index"`
	EventType            EventType `gorm:"size:64;not null;index"`
	ActorDiscordUserID   string    `gorm:"size:32;index"`
	Body                 string    `gorm:"type:text;not null"`
	MetadataJSON         string    `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt time.Time
}

func (eventRecord) TableName() string { return "ticket_events" }

// transcriptRecord is a row of ticket_transcripts.
type transcriptRecord struct {
	TicketID   string    `gorm:"type:char(26);primaryKey"`
	GuildID    string    `gorm:"type:char(26);not null;index"`
	Content    string    `gorm:"type:longtext;not null"`
	CapturedAt time.Time `gorm:"not null"`
	ExpiresAt  time.Time `gorm:"not null;index"`
}

func (transcriptRecord) TableName() string { return "ticket_transcripts" }

// memberStateRecord is a member's ticket slot in one guild: the ticket, or
// opening reservation, they hold. A closed ticket keeps the slot until its
// thread is gone, so a member never has two. Locking this row serializes a
// member's opens and closes.
type memberStateRecord struct {
	ID                 string `gorm:"type:char(26);primaryKey"`
	GuildID            string `gorm:"type:char(26);not null;uniqueIndex:idx_ticket_member_state,priority:1"`
	OwnerDiscordUserID string `gorm:"size:32;not null;uniqueIndex:idx_ticket_member_state,priority:2"`
	OpenTicketID       string `gorm:"type:char(26);not null"`
	// WindowStartedAt and OpenCount belonged to a daily open limit that no
	// longer applies; the columns stay so existing rows keep loading.
	WindowStartedAt      time.Time `gorm:"not null"`
	OpenCount            int       `gorm:"not null"`
	CreatedAt, UpdatedAt time.Time
}

func (memberStateRecord) TableName() string { return "ticket_member_states" }

// Store persists tickets, their timelines, transcripts, and message
// journal.
type Store struct{ db *gorm.DB }

// NewStore returns a Store over db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Models returns the ticket tables' records. The store migrates them with the
// rest of the schema.
func Models() []any {
	return []any{&ticketRecord{}, &eventRecord{}, &transcriptRecord{}, &memberStateRecord{}, &journalRecord{}}
}

// reserveOpening claims the member's ticket slot before any Discord thread
// is created, and returns the token that becomes the ticket's ID.
func (s *Store) reserveOpening(ctx context.Context, actor modules.Actor, now time.Time) (string, error) {
	if actor.GuildID == "" || actor.DiscordUserID == "" {
		return "", errors.New("ticket member is required")
	}
	token := ulid.Make().String()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockMemberState(tx, actor.GuildID, actor.DiscordUserID, now)
		if err != nil {
			return err
		}
		if state.OpenTicketID != "" {
			var count int64
			err := tx.Model(&ticketRecord{}).Where("id = ?", state.OpenTicketID).Count(&count).Error
			if err != nil {
				return err
			}
			// A ticket always blocks; a reservation blocks until it expires.
			if count != 0 || now.Sub(state.UpdatedAt) < openingTTL {
				return ErrDuplicateOpen
			}
		}
		return tx.Model(state).Updates(map[string]any{"open_ticket_id": token, "updated_at": now}).Error
	})
	return token, err
}

// finishOpening turns the member's current reservation into a ticket in
// channelID with its first timeline entry; the slot stays held. A token
// whose reservation expired or was replaced gets ErrInvalidTransition.
func (s *Store) finishOpening(ctx context.Context, actor modules.Actor, token, channelID string, now time.Time) (*Ticket, error) {
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		state, err := lockMemberState(tx, actor.GuildID, actor.DiscordUserID, now)
		if err != nil {
			return err
		}
		if state.OpenTicketID != token || now.Sub(state.UpdatedAt) >= openingTTL {
			return ErrInvalidTransition
		}
		record := ticketRecord{
			ID:                     token,
			GuildID:                actor.GuildID,
			OwnerDiscordUserID:     actor.DiscordUserID,
			ThreadDiscordChannelID: channelID,
			Status:                 StatusOpen,
			MetadataJSON:           "{}",
			CreatedAt:              now,
			UpdatedAt:              now,
		}
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

// releaseOpening frees the member's slot after a failed opening, but only
// while token still holds it and never once its ticket exists.
func (s *Store) releaseOpening(ctx context.Context, actor modules.Actor, token string) error {
	return s.db.WithContext(ctx).Model(&memberStateRecord{}).
		Where("guild_id = ? AND owner_discord_user_id = ? AND open_ticket_id = ?",
			actor.GuildID, actor.DiscordUserID, token).
		Where("NOT EXISTS (SELECT 1 FROM tickets WHERE tickets.id = ?)", token).
		Update("open_ticket_id", "").Error
}

// captureClosure resolves an open ticket and saves its transcript in one
// transaction. The journal's text now expires with the transcript. The
// owner keeps their slot until finishClosure.
func (s *Store) captureClosure(ctx context.Context, guildID, ticketID, actorID string, transcript Transcript, now time.Time) (*Ticket, error) {
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ticketRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("guild_id = ? AND id = ?", guildID, ticketID).First(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if record.Status != StatusOpen {
			return ErrInvalidTransition
		}
		record.Status, record.UpdatedAt = StatusResolved, now
		record.ResolvedByDiscordUserID, record.ResolvedAt = actorID, &now
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		if err := appendEvent(tx, record, EventResolved, actorID, "Ticket closed", "{}", now); err != nil {
			return err
		}
		err = tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&transcriptRecord{
			TicketID:   record.ID,
			GuildID:    record.GuildID,
			Content:    transcript.Content,
			CapturedAt: transcript.CapturedAt,
			ExpiresAt:  transcript.ExpiresAt,
		}).Error
		if err != nil {
			return err
		}
		err = tx.Model(&journalRecord{}).
			Where("guild_id = ? AND ticket_id = ?", guildID, ticketID).
			Update("expires_at", transcript.ExpiresAt).Error
		if err != nil {
			return err
		}
		ticket = ticketFromRecord(record)
		return nil
	})
	return &ticket, err
}

// finishClosure frees the owner's slot once the thread is gone. It only
// frees a slot this ticket still holds, so a late retry cannot release a
// newer ticket.
func (s *Store) finishClosure(ctx context.Context, guildID, ticketID string, now time.Time) error {
	ticket, err := s.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusResolved {
		return ErrInvalidTransition
	}
	return s.db.WithContext(ctx).Model(&memberStateRecord{}).
		Where("guild_id = ? AND owner_discord_user_id = ? AND open_ticket_id = ?",
			guildID, ticket.OwnerDiscordUserID, ticketID).
		Updates(map[string]any{"open_ticket_id": "", "updated_at": now}).Error
}

// get returns one of the guild's tickets, or ErrNotFound.
func (s *Store) get(ctx context.Context, guildID, ticketID string) (*Ticket, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).
		Where("guild_id = ? AND id = ?", guildID, ticketID).
		Limit(1).Find(&record)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	ticket := ticketFromRecord(record)
	return &ticket, nil
}

// activeForMember returns the ticket holding the member's slot, open or
// still closing, or nil if they hold none or only a reservation.
func (s *Store) activeForMember(ctx context.Context, guildID, memberID string) (*Ticket, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).
		Where("guild_id = ? AND owner_discord_user_id = ?", guildID, memberID).
		Where("id IN (SELECT open_ticket_id FROM ticket_member_states WHERE guild_id = ? AND owner_discord_user_id = ?)",
			guildID, memberID).
		Limit(1).Find(&record)
	if result.Error != nil || result.RowsAffected == 0 {
		return nil, result.Error
	}
	ticket := ticketFromRecord(record)
	return &ticket, nil
}

// append adds an entry to a ticket's timeline.
func (s *Store) append(ctx context.Context, ticket *Ticket, eventType EventType, actorID, body, metadata string, now time.Time) error {
	record := ticketRecord{ID: ticket.ID, GuildID: ticket.GuildID}
	return appendEvent(s.db.WithContext(ctx), record, eventType, actorID, body, metadata, now)
}

// list returns up to limit of the guild's tickets, oldest first, optionally
// only those in status. limit defaults to 50 and is capped at 100.
func (s *Store) list(ctx context.Context, guildID string, status Status, limit int) ([]Ticket, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := s.db.WithContext(ctx).Where("guild_id = ?", guildID)
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var records []ticketRecord
	if err := query.Order("created_at ASC, id ASC").Limit(limit).Find(&records).Error; err != nil {
		return nil, err
	}
	return ticketsFromRecords(records), nil
}

// count returns how many of the guild's tickets are in status.
func (s *Store) count(ctx context.Context, guildID string, status Status) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&ticketRecord{}).
		Where("guild_id = ? AND status = ?", guildID, status).
		Count(&count).Error
	return count, err
}

// timeline returns a ticket's events in order.
func (s *Store) timeline(ctx context.Context, guildID, ticketID string) ([]Event, error) {
	var records []eventRecord
	err := s.db.WithContext(ctx).
		Where("guild_id = ? AND ticket_id = ?", guildID, ticketID).
		Order("created_at ASC, id ASC").
		Find(&records).Error
	if err != nil {
		return nil, err
	}
	events := make([]Event, len(records))
	for i, r := range records {
		events[i] = Event{
			ID:                 r.ID,
			TicketID:           r.TicketID,
			GuildID:            r.GuildID,
			Type:               r.EventType,
			ActorDiscordUserID: r.ActorDiscordUserID,
			Body:               r.Body,
			MetadataJSON:       r.MetadataJSON,
			CreatedAt:          r.CreatedAt,
		}
	}
	return events, nil
}

// transcript returns a ticket's transcript, or ErrNotFound if it has none
// or it expired.
func (s *Store) transcript(ctx context.Context, guildID, ticketID string, now time.Time) (*Transcript, error) {
	var record transcriptRecord
	result := s.db.WithContext(ctx).
		Where("guild_id = ? AND ticket_id = ? AND expires_at > ?", guildID, ticketID, now).
		Limit(1).Find(&record)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return &Transcript{
		TicketID:   record.TicketID,
		GuildID:    record.GuildID,
		Content:    record.Content,
		CapturedAt: record.CapturedAt,
		ExpiresAt:  record.ExpiresAt,
	}, nil
}

// purgeExpired deletes every transcript and journal entry past its expiry
// and returns how many transcripts went.
func (s *Store) purgeExpired(ctx context.Context, now time.Time) (int64, error) {
	var count int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(&journalRecord{}).Error; err != nil {
			return err
		}
		result := tx.Where("expires_at <= ?", now).Delete(&transcriptRecord{})
		count = result.RowsAffected
		return result.Error
	})
	return count, err
}

// saveEntryPanel records the posted entry panel in the current settings.
// The row lock and channel check keep a slow setup from overwriting a newer
// entry channel choice.
func (s *Store) saveEntryPanel(ctx context.Context, guildID, channelID, messageID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("guild_id = ? AND module_id = ?", guildID, modules.Tickets).First(&config).Error
		if err != nil {
			return err
		}
		settings := Defaults()
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return err
		}
		if settings.EntryChannelDiscordID != channelID {
			return errors.New("ticket entry channel changed during setup")
		}
		settings.EntryPanelChannelID, settings.EntryPanelMessageID = channelID, messageID
		payload, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		return tx.Model(&config).Update("config_json", string(payload)).Error
	})
}

// ticketIDByChannel returns the ID of the guild's ticket, in any status, in
// channelID, or ErrNotFound.
func (s *Store) ticketIDByChannel(ctx context.Context, guildID, channelID string) (string, error) {
	var record ticketRecord
	result := s.db.WithContext(ctx).Select("id").
		Where("guild_id = ? AND thread_discord_channel_id = ?", guildID, channelID).
		Limit(1).Find(&record)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", ErrNotFound
	}
	return record.ID, nil
}

// openThreadsAfter pages through a guild's open tickets in ID order, for
// the thread membership repair. Keyset paging means a ticket closing
// mid-scan cannot skip a page. Only the ID, thread, and owner are loaded.
func (s *Store) openThreadsAfter(ctx context.Context, guildID, afterID string, limit int) ([]Ticket, error) {
	var records []ticketRecord
	err := s.db.WithContext(ctx).
		Select("id, thread_discord_channel_id, owner_discord_user_id").
		Where("guild_id = ? AND status = ? AND id > ?", guildID, StatusOpen, afterID).
		Order("id ASC").Limit(limit).Find(&records).Error
	if err != nil {
		return nil, err
	}
	return ticketsFromRecords(records), nil
}

// lockMemberState returns the member's state row, creating it if needed,
// locked for the rest of tx.
func lockMemberState(tx *gorm.DB, guildID, ownerID string, now time.Time) (*memberStateRecord, error) {
	seed := memberStateRecord{
		ID:                 ulid.Make().String(),
		GuildID:            guildID,
		OwnerDiscordUserID: ownerID,
		WindowStartedAt:    now,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "guild_id"}, {Name: "owner_discord_user_id"}},
		DoNothing: true,
	}).Create(&seed).Error
	if err != nil {
		return nil, err
	}
	var state memberStateRecord
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("guild_id = ? AND owner_discord_user_id = ?", guildID, ownerID).
		First(&state).Error
	if err != nil {
		return nil, err
	}
	return &state, nil
}

// appendEvent inserts a timeline entry for ticket.
func appendEvent(db *gorm.DB, ticket ticketRecord, eventType EventType, actorID, body, metadata string, now time.Time) error {
	return db.Create(&eventRecord{
		ID:                 ulid.Make().String(),
		TicketID:           ticket.ID,
		GuildID:            ticket.GuildID,
		EventType:          eventType,
		ActorDiscordUserID: actorID,
		Body:               body,
		MetadataJSON:       metadata,
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error
}

// ticketFromRecord converts a row, exposing only the close notice outcome
// from its metadata.
func ticketFromRecord(r ticketRecord) Ticket {
	var metadata struct {
		CloseNotice closeNoticeState `json:"close_notice"`
	}
	_ = json.Unmarshal([]byte(r.MetadataJSON), &metadata)
	return Ticket{
		ID:                      r.ID,
		GuildID:                 r.GuildID,
		OwnerDiscordUserID:      r.OwnerDiscordUserID,
		ThreadDiscordChannelID:  r.ThreadDiscordChannelID,
		Status:                  r.Status,
		ResolvedByDiscordUserID: r.ResolvedByDiscordUserID,
		ResolvedAt:              r.ResolvedAt,
		LogChannelDiscordID:     r.LogChannelDiscordID,
		LogMessageDiscordID:     r.LogMessageDiscordID,
		TranscriptURL:           r.TranscriptURL,
		CloseNoticeDelivered:    metadata.CloseNotice.State == noticeSent,
		CreatedAt:               r.CreatedAt,
		UpdatedAt:               r.UpdatedAt,
	}
}

func ticketsFromRecords(records []ticketRecord) []Ticket {
	tickets := make([]Ticket, len(records))
	for i, record := range records {
		tickets[i] = ticketFromRecord(record)
	}
	return tickets
}
