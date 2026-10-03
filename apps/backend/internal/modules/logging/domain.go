// Package logging is the optional general-logging module. It posts guild
// events (message edits and deletions, joins and leaves, bans, guild and
// channel changes) to staff-only channels the guild picks. It keeps no
// archive: recent messages are cached in memory only so an edit or delete
// can show what changed.
package logging

import (
	"errors"
	"strings"
)

// EventType is a kind of event a guild can route to a channel.
type EventType string

const (
	MessageEdit       EventType = "message_edit"
	MessageDelete     EventType = "message_delete"
	MessageBulkDelete EventType = "message_bulk_delete"
	MemberJoin        EventType = "member_join"
	MemberLeave       EventType = "member_leave"
	DiscordBan        EventType = "discord_ban"
	DiscordUnban      EventType = "discord_unban"
	GuildChange       EventType = "guild_change"
	ChannelChange     EventType = "channel_change"
)

var (
	// ErrDisabled reports a guild with general logging off.
	ErrDisabled = errors.New("general logging module is disabled")
	// ErrPermissionDenied reports a caller without Manage Guild.
	ErrPermissionDenied = errors.New("general logging permission denied")
	// ErrNoDestination reports an event type with no channel routed.
	ErrNoDestination = errors.New("general logging destination is not configured")
)

// Settings are a guild's logging settings: where each event type goes, what
// message details to include, and the cache and retry bounds.
type Settings struct {
	Channels                  map[EventType]string `json:"channels"`
	IncludeMessageContent     bool                 `json:"include_message_content"`
	IncludeAttachmentMetadata bool                 `json:"include_attachment_metadata"`
	IncludeEmbedMetadata      bool                 `json:"include_embed_metadata"`
	CacheEntriesPerGuild      int                  `json:"cache_entries_per_guild"`
	MaxDeliveryAttempts       int                  `json:"max_delivery_attempts"`
}

// Defaults returns the settings a guild starts with: no routes and no
// message content.
func Defaults() Settings {
	return Settings{Channels: map[EventType]string{}, CacheEntriesPerGuild: 1000, MaxDeliveryAttempts: 3}
}

// AttachmentMetadata describes an attachment without its content.
type AttachmentMetadata struct {
	Filename, ContentType string
	Size                  int64
}

// Event is one Discord event to log. It is delivered and then forgotten.
type Event struct {
	GuildID, ChannelDiscordID, MessageDiscordID, ActorDiscordUserID string
	Type                                                            EventType
	Before, After                                                   string
	Attachments                                                     []AttachmentMetadata
	EmbedTypes                                                      []string
	Metadata                                                        map[string]string
	// MessageIDs are the deleted messages of a MessageBulkDelete.
	MessageIDs []string
}

// validateSettings checks settings; enabled logging also needs at least
// one destination.
func validateSettings(settings Settings, enabled bool) error {
	if settings.CacheEntriesPerGuild < 1 || settings.CacheEntriesPerGuild > 10000 {
		return errors.New("message cache limit must be 1 to 10000 entries per guild")
	}
	if settings.MaxDeliveryAttempts < 1 || settings.MaxDeliveryAttempts > 5 {
		return errors.New("delivery attempts must be 1 to 5")
	}
	if enabled && len(settings.Channels) == 0 {
		return errors.New("at least one staff-only destination is required")
	}
	for eventType, channelID := range settings.Channels {
		if !validEventType(eventType) || strings.TrimSpace(channelID) == "" {
			return errors.New("invalid logging event route")
		}
	}
	return nil
}

// validEventType reports whether t is an event the module can log.
func validEventType(t EventType) bool {
	switch t {
	case MessageEdit, MessageDelete, MessageBulkDelete, MemberJoin, MemberLeave, DiscordBan, DiscordUnban, GuildChange, ChannelChange:
		return true
	}
	return false
}
