package honeypot

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Trigger is one message that hit a trap and what came of it. Message
// content is never stored.
type Trigger struct {
	ID                   string    `gorm:"type:char(26);primaryKey"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_honeypot_trigger,priority:1;index"`
	ChannelDiscordID     string    `gorm:"size:32;not null"`
	MessageDiscordID     string    `gorm:"size:32;not null;uniqueIndex:idx_honeypot_trigger,priority:2"`
	TargetDiscordUserID  string    `gorm:"size:32;not null"`
	TemplateID           string    `gorm:"type:char(26);not null"`
	CaseID               string    `gorm:"type:char(26)"`
	Outcome              Outcome   `gorm:"size:32;not null"`
	FailureCode          string    `gorm:"size:64"`
	CreatedAt, UpdatedAt time.Time `gorm:"not null"`
}

// TableName returns the honeypot_triggers table.
func (Trigger) TableName() string { return "honeypot_triggers" }

// Store persists triggers.
type Store struct{ db *gorm.DB }

// NewStore returns a Store over db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Models returns the honeypot table's record. The store migrates it with the
// rest of the schema.
func Models() []any {
	return []any{&Trigger{}}
}

// Claim records a message before anything acts on it, and reports false if
// it was already claimed, so a gateway replay cannot open a second case.
func (s *Store) Claim(ctx context.Context, message Message, templateID string, outcome Outcome) (*Trigger, bool, error) {
	now := time.Now().UTC()
	trigger := Trigger{
		ID:                  ulid.Make().String(),
		GuildID:             message.GuildID,
		ChannelDiscordID:    message.ChannelDiscordID,
		MessageDiscordID:    message.MessageDiscordID,
		TargetDiscordUserID: message.AuthorDiscordUserID,
		TemplateID:          templateID,
		Outcome:             outcome,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&trigger)
	if result.Error != nil {
		return nil, false, result.Error
	}
	return &trigger, result.RowsAffected == 1, nil
}

// Complete records a pending trigger's outcome. A trigger completes once;
// a second completion gets ErrDuplicate.
func (s *Store) Complete(ctx context.Context, id string, outcome Outcome, caseID, failureCode string) error {
	result := s.db.WithContext(ctx).Model(&Trigger{}).
		Where("id = ? AND outcome = ?", id, OutcomePending).
		Updates(map[string]any{
			"outcome":      outcome,
			"case_id":      caseID,
			"failure_code": failureCode,
			"updated_at":   time.Now().UTC(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrDuplicate
	}
	return nil
}

// Statistics counts a guild's triggers by outcome.
func (s *Store) Statistics(ctx context.Context, guildID string) (Statistics, error) {
	var rows []struct {
		Outcome Outcome
		Count   uint64
	}
	err := s.db.WithContext(ctx).Model(&Trigger{}).
		Select("outcome, count(*) AS count").
		Where("guild_id = ?", guildID).
		Group("outcome").
		Scan(&rows).Error
	if err != nil {
		return Statistics{}, err
	}
	var stats Statistics
	for _, row := range rows {
		stats.Total += row.Count
		switch row.Outcome {
		case OutcomePending:
			stats.Pending += row.Count
		case OutcomeCreated:
			stats.Created += row.Count
		case OutcomeFailed:
			stats.Failed += row.Count
		case OutcomeExempt:
			stats.Exempt += row.Count
		}
	}
	return stats, nil
}
