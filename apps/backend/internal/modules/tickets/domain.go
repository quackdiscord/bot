// Package tickets is the optional support-ticket module. /setup tickets posts
// an entry panel with an Open ticket button; pressing it opens a private
// thread under the entry channel and posts the ticket to a staff queue
// channel. Closing locks the thread, saves a transcript merged from the
// message journal and the surviving history, attaches it to the queue post,
// DMs the member a copy, and deletes the thread. Tickets are separate from
// cases and appeals.
package tickets

import (
	"errors"
	"strings"
	"time"
)

// Status is where a ticket is in its lifecycle.
type Status string

const (
	// StatusOpen is a ticket whose thread is still live.
	StatusOpen Status = "open"
	// StatusResolved is a closed ticket. Closed tickets cannot be reopened.
	StatusResolved Status = "resolved"
	// StatusCancelled is a ticket withdrawn under the old lifecycle. New
	// closes always resolve.
	StatusCancelled Status = "cancelled"
)

// EventType is the kind of a ticket timeline entry.
type EventType string

// Timeline entries. EventReplied and EventReopened only appear on tickets
// from before the current lifecycle.
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
	// ErrDuplicateOpen reports that the member already holds a ticket, open
	// or still closing.
	ErrDuplicateOpen = errors.New("member already has an open ticket")
	// ErrInvalidTransition reports an operation the ticket's status does not
	// allow.
	ErrInvalidTransition = errors.New("invalid ticket transition")
)

// Settings are a guild's ticket settings, stored as the module's config
// JSON. The entry panel fields are bookkeeping for the posted panel, so
// setup can edit it in place or retire it when the entry channel moves.
type Settings struct {
	EntryPanelMessageID     string `json:"entry_panel_message_id,omitempty"`
	EntryPanelChannelID     string `json:"entry_panel_channel_id,omitempty"`
	QueueChannelDiscordID   string `json:"queue_channel_discord_id"`
	EntryChannelDiscordID   string `json:"entry_channel_discord_id"`
	TranscriptRetentionDays int    `json:"transcript_retention_days"`
}

// Defaults returns the settings a guild starts with.
func Defaults() Settings {
	return Settings{TranscriptRetentionDays: 90}
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
	// LogChannelDiscordID and LogMessageDiscordID locate the staff queue
	// post, when one is known. The post is best-effort, so they may be empty.
	LogChannelDiscordID string `json:"log_channel_discord_id,omitempty"`
	LogMessageDiscordID string `json:"log_message_discord_id,omitempty"`
	// TranscriptURL links the queue post carrying the transcript, once it
	// does, so a resumed close does not post it again.
	TranscriptURL string `json:"transcript_url,omitempty"`
	// CloseNoticeDelivered reports a confirmed close DM to the member.
	CloseNoticeDelivered bool      `json:"-"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
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

// QueueReceipt identifies a delivered staff queue post and links it.
type QueueReceipt struct {
	MessageID string
	URL       string
}

// TranscriptAttachment describes a file on a ticket message. Only the
// description is kept; the original URL may expire.
type TranscriptAttachment struct {
	Name string
	Size int
	URL  string
}

// TranscriptMessage is one ticket message, as journaled when it arrived or
// as it survives in the thread at close.
type TranscriptMessage struct {
	MessageID, AuthorID, AuthorName, Body string
	SentAt                                time.Time
	Attachments                           []TranscriptAttachment
}

// validateSettings checks settings; enabled tickets also need an entry and
// a queue channel.
func validateSettings(settings Settings, enabled bool) error {
	if enabled && strings.TrimSpace(settings.EntryChannelDiscordID) == "" {
		return errors.New("entry channel is required when tickets are enabled")
	}
	if enabled && strings.TrimSpace(settings.QueueChannelDiscordID) == "" {
		return errors.New("staff queue channel is required when tickets are enabled")
	}
	if settings.TranscriptRetentionDays < 1 || settings.TranscriptRetentionDays > 365 {
		return errors.New("transcript retention must be 1 to 365 days")
	}
	return nil
}
