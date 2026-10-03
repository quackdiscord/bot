package tickets

import (
	"context"
	"errors"
	"slices"
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
	ID                      string `gorm:"type:char(26);primaryKey"`
	GuildID                 string `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string `gorm:"size:32;uniqueIndex"`
	Status                  Status `gorm:"size:32;not null;index:idx_ticket_guild_status,priority:2"`
	LogMessageDiscordID     string `gorm:"size:32"`
	ResolvedByDiscordUserID string `gorm:"size:32"`
	ResolvedAt              *time.Time
	TranscriptURL           string `gorm:"size:1024"`
	MetadataJSON            string `gorm:"type:json;not null"`
	CreatedAt, UpdatedAt    time.Time
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

// memberStateRecord is a member's ticket allowance in one guild: the ticket
// (or opening reservation) they hold, and how many they opened in the
// current 24-hour window. Locking this row serializes a member's opens.
type memberStateRecord struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_ticket_member_state,priority:1"`
	OwnerDiscordUserID   string    `gorm:"size:32;not null;uniqueIndex:idx_ticket_member_state,priority:2"`
	OpenTicketID         string    `gorm:"type:char(26);not null"`
	WindowStartedAt      time.Time `gorm:"not null"`
	OpenCount            int       `gorm:"not null"`
	CreatedAt, UpdatedAt time.Time
}

func (memberStateRecord) TableName() string { return "ticket_member_states" }

// lifecycle is the ticket state machine: for each status a ticket can move
// to, the statuses it may move from and the timeline entry the move leaves.
var lifecycle = map[Status]struct {
	from  []Status
	event EventType
	body  string
}{
	StatusResolved:  {[]Status{StatusOpen}, EventResolved, "Ticket resolved"},
	StatusCancelled: {[]Status{StatusOpen}, EventCancelled, "Ticket cancelled"},
	StatusOpen:      {[]Status{StatusResolved, StatusCancelled}, EventReopened, "Ticket reopened"},
}

// Store persists tickets, their append-only timelines, and transcripts.
type Store struct{ db *gorm.DB }

// NewStore returns a Store over db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Models returns the ticket tables' records. The store migrates them with the
// rest of the schema.
func Models() []any {
	return []any{&ticketRecord{}, &eventRecord{}, &transcriptRecord{}, &memberStateRecord{}}
}

// reserveOpening claims the member's ticket slot and one of their daily
// opens before any Discord channel is created, and returns the token that
// becomes the ticket's ID. A failed opening keeps its daily count, so a
// member cannot force unbounded provisioning by retrying failures.
func (s *Store) reserveOpening(ctx context.Context, actor modules.Actor, dailyLimit int, now time.Time) (string, error) {
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
			// A committed ticket always blocks; a reservation blocks
			// until it expires.
			if count != 0 || now.Sub(state.UpdatedAt) < openingTTL {
				return ErrDuplicateOpen
			}
		}
		if now.Sub(state.WindowStartedAt) >= 24*time.Hour {
			state.WindowStartedAt, state.OpenCount = now, 0
		}
		if state.OpenCount >= dailyLimit {
			return ErrRateLimited
		}
		return tx.Model(state).Updates(map[string]any{
			"open_ticket_id":    token,
			"open_count":        state.OpenCount + 1,
			"window_started_at": state.WindowStartedAt,
			"updated_at":        now,
		}).Error
	})
	return token, err
}

// finishOpening turns the member's current reservation into a ticket in
// channelID with its first timeline entry. A token whose reservation expired
// or was replaced gets ErrInvalidTransition.
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
		if err := appendEvent(tx, Event{
			TicketID:           record.ID,
			GuildID:            record.GuildID,
			Type:               EventOpened,
			ActorDiscordUserID: actor.DiscordUserID,
			Body:               "Ticket opened",
			MetadataJSON:       "{}",
			CreatedAt:          now,
		}); err != nil {
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

// transition moves a ticket to status to, as lifecycle allows, and records
// the move on its timeline. It keeps the owner's member slot in step: a
// reopened ticket takes the slot back, failing with ErrDuplicateOpen if the
// owner has opened another since. A non-nil transcript is saved, replacing
// any earlier one.
func (s *Store) transition(ctx context.Context, guildID, ticketID string, to Status, actorID string, transcript *Transcript, now time.Time) (*Ticket, error) {
	rule := lifecycle[to]
	var ticket Ticket
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var record ticketRecord
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("guild_id = ? AND id = ?", guildID, ticketID).
			First(&record).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !slices.Contains(rule.from, record.Status) {
			return ErrInvalidTransition
		}
		state, err := lockMemberState(tx, record.GuildID, record.OwnerDiscordUserID, now)
		if err != nil {
			return err
		}
		if to == StatusOpen {
			if state.OpenTicketID != "" && state.OpenTicketID != record.ID {
				return ErrDuplicateOpen
			}
			state.OpenTicketID = record.ID
		} else if state.OpenTicketID == record.ID {
			state.OpenTicketID = ""
		}
		state.UpdatedAt = now
		record.Status, record.UpdatedAt = to, now
		if to == StatusResolved {
			record.ResolvedByDiscordUserID, record.ResolvedAt = actorID, &now
		} else {
			record.ResolvedByDiscordUserID, record.ResolvedAt = "", nil
		}
		if err := tx.Save(&record).Error; err != nil {
			return err
		}
		if err := tx.Save(state).Error; err != nil {
			return err
		}
		if err := appendEvent(tx, Event{
			TicketID:           record.ID,
			GuildID:            record.GuildID,
			Type:               rule.event,
			ActorDiscordUserID: actorID,
			Body:               rule.body,
			MetadataJSON:       "{}",
			CreatedAt:          now,
		}); err != nil {
			return err
		}
		if transcript != nil {
			err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&transcriptRecord{
				TicketID:   record.ID,
				GuildID:    record.GuildID,
				Content:    transcript.Content,
				CapturedAt: transcript.CapturedAt,
				ExpiresAt:  transcript.ExpiresAt,
			}).Error
			if err != nil {
				return err
			}
		}
		ticket = ticketFromRecord(record)
		return nil
	})
	return &ticket, err
}

// append adds an entry to a ticket's timeline.
func (s *Store) append(ctx context.Context, event Event) error {
	return appendEvent(s.db.WithContext(ctx), event)
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

// purgeExpiredTranscripts deletes every transcript past its expiry and
// returns how many.
func (s *Store) purgeExpiredTranscripts(ctx context.Context, now time.Time) (int64, error) {
	result := s.db.WithContext(ctx).Where("expires_at <= ?", now).Delete(&transcriptRecord{})
	return result.RowsAffected, result.Error
}

// ticketIDByChannel returns the ID of the guild's ticket in channelID, or ""
// if the channel is not a ticket.
func (s *Store) ticketIDByChannel(ctx context.Context, guildID, channelID string) (string, error) {
	var record ticketRecord
	err := s.db.WithContext(ctx).Select("id").
		Where("guild_id = ? AND thread_discord_channel_id = ?", guildID, channelID).
		Limit(1).Find(&record).Error
	return record.ID, err
}

// threadsAfter pages through a guild's tickets in ID order, for the thread
// membership repair. Only the ID, channel, and owner are loaded.
func (s *Store) threadsAfter(ctx context.Context, guildID, afterID string, limit int) ([]Ticket, error) {
	var records []ticketRecord
	err := s.db.WithContext(ctx).
		Select("id, thread_discord_channel_id, owner_discord_user_id").
		Where("guild_id = ? AND id > ?", guildID, afterID).
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

// appendEvent inserts event, assigning its ID.
func appendEvent(db *gorm.DB, event Event) error {
	return db.Create(&eventRecord{
		ID:                 ulid.Make().String(),
		TicketID:           event.TicketID,
		GuildID:            event.GuildID,
		EventType:          event.Type,
		ActorDiscordUserID: event.ActorDiscordUserID,
		Body:               event.Body,
		MetadataJSON:       event.MetadataJSON,
		CreatedAt:          event.CreatedAt,
		UpdatedAt:          event.CreatedAt,
	}).Error
}

func ticketFromRecord(r ticketRecord) Ticket {
	return Ticket{
		ID:                      r.ID,
		GuildID:                 r.GuildID,
		OwnerDiscordUserID:      r.OwnerDiscordUserID,
		ThreadDiscordChannelID:  r.ThreadDiscordChannelID,
		Status:                  r.Status,
		ResolvedByDiscordUserID: r.ResolvedByDiscordUserID,
		ResolvedAt:              r.ResolvedAt,
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
