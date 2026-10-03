// Package modules holds what the optional modules (tickets, general logging,
// and honeypots) share: each guild's on/off switch and settings for them, the
// actor they authorize, the mapping between Discord and internal guild IDs,
// the adapter that writes their events to the core audit log, HTTP helpers,
// and a bounded pool for their gateway work.
package modules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// ID names a module in module_configurations.
type ID string

const (
	// Tickets is the private support-ticket module.
	Tickets ID = "tickets"
	// GeneralLogging is the staff-only Discord event log.
	GeneralLogging ID = "general_logging"
	// Honeypots is the trap-channel module that opens cases automatically.
	Honeypots ID = "honeypots"
)

// SystemActorID is the actor recorded for changes Quack makes on its own,
// such as repairing a module after its channel is deleted.
const SystemActorID = "quack-system"

// Configuration is one guild's switch and settings for one module. The
// settings are opaque JSON that only the module itself interprets.
type Configuration struct {
	ID         string    `gorm:"type:char(26);primaryKey" json:"id"`
	GuildID    string    `gorm:"type:char(26);not null;uniqueIndex:idx_module_configuration,priority:1" json:"guild_id"`
	ModuleID   ID        `gorm:"size:64;not null;uniqueIndex:idx_module_configuration,priority:2" json:"module_id"`
	Enabled    bool      `gorm:"not null;default:false" json:"enabled"`
	ConfigJSON string    `gorm:"type:json;not null" json:"-"`
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

// TableName returns the module_configurations table.
func (Configuration) TableName() string { return "module_configurations" }

// Models returns the registry's table records. The store migrates them with
// the rest of the schema.
func Models() []any {
	return []any{&Configuration{}}
}

// Registry reads and writes module configurations. It is the single source
// of truth for whether a module is on in a guild: the modules read it, and
// it implements quack.ModuleToggles so the core settings API writes it too.
type Registry struct{ db *gorm.DB }

// NewRegistry returns a Registry over db.
func NewRegistry(db *gorm.DB) *Registry { return &Registry{db: db} }

// Configuration returns a guild's configuration for a module, or nil if the
// guild has never configured it.
func (r *Registry) Configuration(ctx context.Context, guildID string, id ID) (*Configuration, error) {
	return configuration(r.db.WithContext(ctx), guildID, id)
}

// SetConfiguration creates or replaces a guild's configuration for a module.
// An empty ConfigJSON is stored as {}.
func (r *Registry) SetConfiguration(ctx context.Context, c Configuration) (*Configuration, error) {
	if c.GuildID == "" {
		return nil, errors.New("guild id is required")
	}
	if c.ConfigJSON == "" {
		c.ConfigJSON = "{}"
	}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return put(tx, &c)
	})
	if err != nil {
		return nil, fmt.Errorf("save %s configuration: %w", c.ModuleID, err)
	}
	return &c, nil
}

// SaveSettings encodes a module's settings and stores them with its switch.
func (r *Registry) SaveSettings(ctx context.Context, guildID string, id ID, enabled bool, settings any) (*Configuration, error) {
	payload, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("encode %s settings: %w", id, err)
	}
	return r.SetConfiguration(ctx, Configuration{
		GuildID:    guildID,
		ModuleID:   id,
		Enabled:    enabled,
		ConfigJSON: string(payload),
	})
}

// LoadSettings decodes a guild's settings for a module over defaults and
// reports whether the module is on. A guild that never configured the
// module gets defaults, switched off. Pass a fresh defaults value: decoding
// writes into its maps and slices.
func LoadSettings[T any](ctx context.Context, r *Registry, guildID string, id ID, defaults T) (T, bool, error) {
	c, err := r.Configuration(ctx, guildID, id)
	if err != nil {
		var zero T
		return zero, false, err
	}
	if c == nil {
		return defaults, false, nil
	}
	settings := defaults
	if err := json.Unmarshal([]byte(c.ConfigJSON), &settings); err != nil {
		var zero T
		return zero, false, fmt.Errorf("decode %s settings: %w", id, err)
	}
	return settings, c.Enabled, nil
}

// AnyEnabled reports whether any guild has the module on. Startup uses it to
// decide which gateway intents to request.
func (r *Registry) AnyEnabled(ctx context.Context, id ID) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&Configuration{}).
		Where("module_id = ? AND enabled = ?", id, true).
		Count(&count).Error
	return count > 0, err
}

// ModuleStates returns which modules the guild has on.
func (r *Registry) ModuleStates(ctx context.Context, guildID string) (quack.ModuleStates, error) {
	var rows []Configuration
	err := r.db.WithContext(ctx).
		Where("guild_id = ? AND enabled = ?", guildID, true).
		Find(&rows).Error
	if err != nil {
		return quack.ModuleStates{}, err
	}
	var states quack.ModuleStates
	for _, row := range rows {
		switch row.ModuleID {
		case Tickets:
			states.Tickets = true
		case GeneralLogging:
			states.GeneralLogging = true
		case Honeypots:
			states.Honeypot = true
		}
	}
	return states, nil
}

// SetModuleStates switches the guild's modules on or off, keeping each
// module's settings. A module the guild never configured starts from its
// defaults, and is not created at all just to be switched off.
func (r *Registry) SetModuleStates(ctx context.Context, guildID string, states quack.ModuleStates) error {
	wanted := []struct {
		id      ID
		enabled bool
	}{
		{Tickets, states.Tickets},
		{GeneralLogging, states.GeneralLogging},
		{Honeypots, states.Honeypot},
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, want := range wanted {
			current, err := configuration(tx, guildID, want.id)
			if err != nil {
				return err
			}
			switch {
			case current == nil && !want.enabled:
				continue
			case current == nil:
				current = &Configuration{GuildID: guildID, ModuleID: want.id, ConfigJSON: "{}"}
			case current.Enabled == want.enabled:
				continue
			}
			current.Enabled = want.enabled
			if err := put(tx, current); err != nil {
				return fmt.Errorf("switch %s: %w", want.id, err)
			}
		}
		return nil
	})
}

// configuration loads one guild's row for a module, or nil if there is none.
func configuration(db *gorm.DB, guildID string, id ID) (*Configuration, error) {
	var c Configuration
	result := db.Where("guild_id = ? AND module_id = ?", guildID, id).Limit(1).Find(&c)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return &c, nil
}

// put upserts c by guild and module, keeping the row's ID and creation time.
func put(tx *gorm.DB, c *Configuration) error {
	existing, err := configuration(tx, c.GuildID, c.ModuleID)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if existing == nil {
		c.ID, c.CreatedAt = ulid.Make().String(), now
	} else {
		c.ID, c.CreatedAt = existing.ID, existing.CreatedAt
	}
	c.UpdatedAt = now
	return tx.Save(c).Error
}
