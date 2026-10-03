// Package tickets is the optional support-ticket module. A member opens a
// ticket from a button in the entry channel and gets a private thread (or
// text channel) shared with staff. Closing a ticket saves its transcript for
// a bounded retention period; the ticket's timeline is kept.
package tickets

import (
	"errors"
	"time"
)

// Status is where a ticket is in its lifecycle.
type Status string

const (
	// StatusOpen accepts member and staff replies.
	StatusOpen Status = "open"
	// StatusResolved is a staff-completed ticket eligible for bounded reopen.
	StatusResolved Status = "resolved"
	// StatusCancelled is an owner- or staff-cancelled ticket.
	StatusCancelled Status = "cancelled"
)

// EventType is the kind of a ticket timeline entry.
type EventType string

const (
	EventOpened              EventType = "opened"
	EventReplied             EventType = "replied"
	EventResolved            EventType = "resolved"
	EventCancelled           EventType = "cancelled"
	EventReopened            EventType = "reopened"
	EventChannelMissing      EventType = "channel_missing"
	EventPermissionsRepaired EventType = "permissions_repaired"
)

var (
	// ErrDisabled reports that the guild has not enabled tickets.
	ErrDisabled = errors.New("ticket module is disabled")
	// ErrPermissionDenied reports an unauthorized ticket operation.
	ErrPermissionDenied = errors.New("ticket permission denied")
	// ErrNotFound reports a ticket that does not exist in the guild.
	ErrNotFound = errors.New("ticket not found")
	// ErrDuplicateOpen reports that the member already has an open ticket.
	ErrDuplicateOpen = errors.New("member already has an open ticket")
	// ErrRateLimited reports that the member hit the daily open limit.
	ErrRateLimited = errors.New("ticket open rate limit exceeded")
	// ErrInvalidTransition reports an operation the ticket's status does not
	// allow.
	ErrInvalidTransition = errors.New("invalid ticket transition")
)

// Settings are a guild's ticket settings, stored as the module's config
// JSON.
type Settings struct {
	EntryChannelDiscordID string   `json:"entry_channel_discord_id"`
	StaffRoleDiscordIDs   []string `json:"staff_role_discord_ids"`
	// UsePrivateThreads creates tickets under the entry channel when enabled.
	UsePrivateThreads       bool `json:"use_private_threads"`
	TranscriptRetentionDays int  `json:"transcript_retention_days"`
	DailyOpenLimit          int  `json:"daily_open_limit"`
	ReopenWindowHours       int  `json:"reopen_window_hours"`
}

// Defaults returns the settings a guild starts with.
func Defaults() Settings {
	return Settings{UsePrivateThreads: true, TranscriptRetentionDays: 90, DailyOpenLimit: 3, ReopenWindowHours: 168}
}

// Ticket is one support ticket. Tickets are separate from cases and
// appeals.
type Ticket struct {
	ID                      string     `json:"id"`
	GuildID                 string     `json:"guild_id"`
	OwnerDiscordUserID      string     `json:"owner_discord_user_id"`
	ThreadDiscordChannelID  string     `json:"thread_discord_channel_id"`
	Status                  Status     `json:"status"`
	ResolvedByDiscordUserID string     `json:"resolved_by_discord_user_id,omitempty"`
	ResolvedAt              *time.Time `json:"resolved_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// Event is an entry on a ticket's append-only timeline.
type Event struct {
	ID                 string    `json:"id"`
	TicketID           string    `json:"ticket_id"`
	GuildID            string    `json:"guild_id"`
	Type               EventType `json:"type"`
	ActorDiscordUserID string    `json:"actor_discord_user_id"`
	Body               string    `json:"body"`
	MetadataJSON       string    `json:"metadata_json"`
	CreatedAt          time.Time `json:"created_at"`
}

// Transcript is a closed ticket's messages, kept until ExpiresAt.
type Transcript struct {
	TicketID   string    `json:"ticket_id"`
	GuildID    string    `json:"guild_id"`
	Content    string    `json:"content"`
	CapturedAt time.Time `json:"captured_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// ModuleStatus is a content-free summary of a guild's tickets.
type ModuleStatus struct {
	Enabled         bool  `json:"enabled"`
	EntryConfigured bool  `json:"entry_configured"`
	OpenTickets     int64 `json:"open_tickets"`
}
