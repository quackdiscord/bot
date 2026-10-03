// Package tickets is the optional support-ticket module. A member presses
// the Open ticket button (see EntryComponents) and gets a private thread, or
// text channel, shared with staff. Closing a ticket saves its transcript for
// a bounded retention period; the ticket's timeline is kept. Tickets are
// separate from cases and appeals.
package tickets

import (
	"errors"
	"strings"
	"time"
)

// Status is where a ticket is in its lifecycle.
type Status string

const (
	// StatusOpen accepts member and staff replies.
	StatusOpen Status = "open"
	// StatusResolved is a ticket staff closed as done. Staff may reopen it
	// within the guild's reopen window.
	StatusResolved Status = "resolved"
	// StatusCancelled is a ticket its owner or staff withdrew.
	StatusCancelled Status = "cancelled"
)

// EventType is the kind of a ticket timeline entry.
type EventType string

// Timeline entries. The first five follow the ticket's lifecycle; the last
// two record repairs Quack made to the ticket's Discord channel.
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
	// UsePrivateThreads opens tickets as private threads under the entry
	// channel. When off, each ticket is a private text channel in the entry
	// channel's category.
	UsePrivateThreads       bool `json:"use_private_threads"`
	TranscriptRetentionDays int  `json:"transcript_retention_days"`
	DailyOpenLimit          int  `json:"daily_open_limit"`
	ReopenWindowHours       int  `json:"reopen_window_hours"`
}

// Defaults returns the settings a guild starts with.
func Defaults() Settings {
	return Settings{
		UsePrivateThreads:       true,
		TranscriptRetentionDays: 90,
		DailyOpenLimit:          3,
		ReopenWindowHours:       168,
	}
}

// Ticket is one support ticket.
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

// validateSettings checks settings; enabled tickets also need an entry
// channel and a staff role.
func validateSettings(settings Settings, enabled bool) error {
	if enabled && strings.TrimSpace(settings.EntryChannelDiscordID) == "" {
		return errors.New("entry channel is required when tickets are enabled")
	}
	if enabled && len(settings.StaffRoleDiscordIDs) == 0 {
		return errors.New("at least one staff role is required when tickets are enabled")
	}
	for _, roleID := range settings.StaffRoleDiscordIDs {
		if strings.TrimSpace(roleID) == "" {
			return errors.New("staff role ids cannot be empty")
		}
	}
	if settings.TranscriptRetentionDays < 1 || settings.TranscriptRetentionDays > 365 {
		return errors.New("transcript retention must be 1 to 365 days")
	}
	if settings.DailyOpenLimit < 1 || settings.DailyOpenLimit > 20 {
		return errors.New("daily open limit must be 1 to 20")
	}
	if settings.ReopenWindowHours < 1 || settings.ReopenWindowHours > 720 {
		return errors.New("reopen window must be 1 to 720 hours")
	}
	return nil
}

// validateReply bounds a reply to the length the reply modal allows.
func validateReply(body string) error {
	if strings.TrimSpace(body) == "" || len(body) > 4000 {
		return errors.New("ticket reply must contain 1 to 4000 characters")
	}
	return nil
}
