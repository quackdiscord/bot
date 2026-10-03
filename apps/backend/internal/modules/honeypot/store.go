package honeypot

import (
	"context"
	"encoding/json"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	// burstWindow groups a member's messages into one incident: a message
	// within this long of their last saved incident joins it.
	burstWindow = 30 * time.Second
	// incidentLease is how long a pending incident belongs to the worker
	// that claimed it, long enough for the case preflight to finish. After
	// that, recovery may take it over. UpdatedAt is the lease epoch.
	incidentLease = time.Minute
	// attemptTimeout bounds one attempt at an incident. It is shorter than
	// incidentLease, so a slow worker gives up before recovery starts.
	attemptTimeout = 45 * time.Second
)

// Trigger is one incident (or exempt message) and what came of it. Message
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

// Store persists triggers, bait-message cleanups, and warning refreshes.
type Store struct{ db *gorm.DB }

// NewStore returns a Store over db.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Models returns the honeypot tables' records. The store migrates them with
// the rest of the schema.
func Models() []any {
	return []any{&Trigger{}, &MessageCleanup{}, &WarningRefresh{}}
}

// Claim records a message before anything acts on it, and reports false if
// it was already claimed, so a gateway replay cannot open a second case.
// On a replay it returns the existing trigger.
func (s *Store) Claim(ctx context.Context, message Message, templateID string, outcome Outcome) (*Trigger, bool, error) {
	// Millisecond precision keeps the lease epoch comparable after a MySQL
	// round trip.
	now := time.Now().UTC().Truncate(time.Millisecond)
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
	if result.RowsAffected == 1 {
		return &trigger, true, nil
	}
	var existing Trigger
	err := s.db.WithContext(ctx).
		Where("guild_id = ? AND message_discord_id = ?", message.GuildID, message.MessageDiscordID).
		First(&existing).Error
	if err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

// Complete records a pending trigger's outcome. A trigger completes once;
// a second completion gets ErrDuplicate.
func (s *Store) Complete(ctx context.Context, id string, outcome Outcome, caseID, failureCode string) error {
	var trigger Trigger
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&trigger).Error; err != nil {
		return err
	}
	return s.completeIncident(ctx, &trigger, outcome, caseID, failureCode)
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

// ClaimIncident claims message for a new incident, or joins it to the
// member's incident already pending or saved within burstWindow, reporting
// true only for a new one. Either way the message is queued for cleanup in
// the same transaction. The guild's module configuration row is locked
// while the settings are checked again, so a claim never outlives an admin
// edit, and claims for the same guild are serialized. A failed incident
// does not absorb later messages, so the next one tries again.
func (s *Store) ClaimIncident(ctx context.Context, message Message, templateID string) (*Trigger, bool, error) {
	var trigger *Trigger
	var claimed bool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var config modules.Configuration
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("guild_id = ? AND module_id = ?", message.GuildID, modules.Honeypots).
			First(&config).Error
		if err != nil {
			return err
		}
		if !config.Enabled {
			return ErrDisabled
		}
		var settings Settings
		if err := json.Unmarshal([]byte(config.ConfigJSON), &settings); err != nil {
			return err
		}
		settings = normalizeSettings(settings)
		if settings.ChannelDiscordID != message.ChannelDiscordID || settings.TemplateID != templateID {
			return ErrNotTrigger
		}
		if isExempt(message) {
			return ErrExempt
		}
		txStore := NewStore(tx)
		// A replayed burst message keeps its original incident even after
		// the window: replaying cleanup must never open another case.
		var cleanup MessageCleanup
		existing := tx.Where("guild_id = ? AND message_discord_id = ?", message.GuildID, message.MessageDiscordID).
			Limit(1).Find(&cleanup)
		if existing.Error != nil {
			return existing.Error
		}
		if existing.RowsAffected > 0 {
			var prior Trigger
			if err := tx.Where("id = ? AND guild_id = ?", cleanup.TriggerID, message.GuildID).First(&prior).Error; err != nil {
				return err
			}
			trigger = &prior
			return nil
		}
		var recent Trigger
		result := tx.Where("guild_id = ? AND target_discord_user_id = ? AND channel_discord_id = ? AND (outcome = ? OR (outcome = ? AND created_at > ?))",
			message.GuildID, message.AuthorDiscordUserID, message.ChannelDiscordID,
			OutcomePending, OutcomeCreated, time.Now().UTC().Add(-burstWindow)).
			Order("created_at DESC").Limit(1).Find(&recent)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			trigger = &recent
			return txStore.scheduleCleanup(ctx, message, recent.ID)
		}
		trigger, claimed, err = txStore.Claim(ctx, message, templateID, OutcomePending)
		if err != nil {
			return err
		}
		return txStore.scheduleCleanup(ctx, message, trigger.ID)
	})
	return trigger, claimed, err
}

// claimPendingIncident leases one pending incident whose lease has expired,
// by moving its epoch forward atomically. It returns nil when none is due.
func (s *Store) claimPendingIncident(ctx context.Context) (*Trigger, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	var candidates []Trigger
	err := s.db.WithContext(ctx).
		Where("outcome = ? AND updated_at <= ?", OutcomePending, now.Add(-incidentLease)).
		Order("updated_at ASC").Limit(10).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		result := s.db.WithContext(ctx).Model(&Trigger{}).
			Where("id = ? AND outcome = ? AND updated_at = ?", candidate.ID, OutcomePending, candidate.UpdatedAt).
			Update("updated_at", now)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 1 {
			candidate.UpdatedAt = now
			return &candidate, nil
		}
	}
	return nil, nil
}

// completeIncident records an incident's outcome, fenced on the lease epoch
// the caller holds, so a timed-out worker cannot overwrite its recovery. A
// created outcome requests a warning refresh in the same transaction.
func (s *Store) completeIncident(ctx context.Context, trigger *Trigger, outcome Outcome, caseID, failureCode string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Trigger{}).
			Where("id = ? AND outcome = ? AND updated_at = ?", trigger.ID, OutcomePending, trigger.UpdatedAt).
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
		if outcome == OutcomeCreated {
			return requestWarningRefresh(ctx, tx, trigger.GuildID)
		}
		return nil
	})
}
