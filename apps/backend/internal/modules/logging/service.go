package logging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// secretPattern matches bot tokens, webhook URLs, and key=value secrets that
// members paste into chat, so they are redacted before reaching a log
// channel.
var secretPattern = regexp.MustCompile(`(?i)(bot\s+[A-Za-z0-9._-]{20,}|https://(?:discord(?:app)?\.com/api/)?webhooks/[^\s]+|(?:token|secret|authorization)\s*[:=]\s*[^\s]+)`)

// DeliveryClient posts log messages. Payloads are already redacted.
type DeliveryClient interface {
	SendStaffLog(ctx context.Context, guildID, channelID, payload string) error
	ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error
}

// Status is a guild's delivery health since the process started.
type Status struct {
	Delivered      uint64     `json:"delivered"`
	Failed         uint64     `json:"failed"`
	LastError      string     `json:"last_error,omitempty"`
	LastFailureAt  *time.Time `json:"last_failure_at,omitempty"`
	CachedMessages int        `json:"cached_messages"`
}

// Service formats, redacts, and delivers events with bounded retries, and
// manages each guild's settings.
type Service struct {
	registry *modules.Registry
	auditor  modules.Auditor
	client   DeliveryClient
	cache    *MessageCache

	mu     sync.Mutex
	status map[string]Status
}

// NewService returns a Service that caches recent messages in cache, or in
// a default-sized cache if cache is nil. A nil auditor only logs settings
// changes.
func NewService(registry *modules.Registry, auditor modules.Auditor, client DeliveryClient, cache *MessageCache) *Service {
	if cache == nil {
		cache = NewMessageCache(defaultCacheLimit)
	}
	return &Service{
		registry: registry,
		auditor:  auditor,
		client:   client,
		cache:    cache,
		status:   make(map[string]Status),
	}
}

// Settings returns the guild's settings, whether logging is on, and its
// delivery status. It needs Manage Guild.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, bool, Status, error) {
	if !actor.CanManage {
		return Settings{}, false, Status{}, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	return settings, enabled, s.Status(actor.GuildID), err
}

// UpdateSettings saves the guild's settings after checking that every
// destination is staff-only. It needs Manage Guild.
func (s *Service) UpdateSettings(ctx context.Context, actor modules.Actor, enabled bool, settings Settings) (Settings, error) {
	const action = "general_logging.settings.update"
	if !actor.CanManage {
		s.audit(ctx, actor, action, "denied", ErrPermissionDenied)
		return Settings{}, ErrPermissionDenied
	}
	if err := validateSettings(settings, enabled); err != nil {
		return Settings{}, err
	}
	for _, channelID := range destinations(settings.Channels) {
		if err := s.client.ValidateStaffOnlyChannel(ctx, actor.GuildID, channelID); err != nil {
			s.audit(ctx, actor, action, "failure", err)
			return Settings{}, err
		}
	}
	if _, err := s.registry.SaveSettings(ctx, actor.GuildID, modules.GeneralLogging, enabled, settings); err != nil {
		return Settings{}, err
	}
	s.cache.SetGuildLimit(actor.GuildID, settings.CacheEntriesPerGuild)
	s.audit(ctx, actor, action, "success", nil)
	return settings, nil
}

// RepairDeletedChannel removes every route to channelID, turning logging
// off if no routes remain. It needs Manage Guild.
func (s *Service) RepairDeletedChannel(ctx context.Context, actor modules.Actor, channelID string) (Settings, bool, error) {
	if !actor.CanManage {
		return Settings{}, false, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return Settings{}, false, err
	}
	maps.DeleteFunc(settings.Channels, func(_ EventType, destination string) bool {
		return destination == channelID
	})
	if len(settings.Channels) == 0 {
		enabled = false
	}
	updated, err := s.UpdateSettings(ctx, actor, enabled, settings)
	if err != nil {
		return Settings{}, false, err
	}
	s.audit(ctx, actor, "general_logging.channel_repair", "success", nil)
	return updated, enabled, nil
}

// Status returns the guild's delivery counters.
func (s *Service) Status(guildID string) Status {
	s.mu.Lock()
	status := s.status[guildID]
	s.mu.Unlock()
	status.CachedMessages = s.cache.Len(guildID)
	return status
}

// CacheMessage caches a message for a guild with logging on.
func (s *Service) CacheMessage(ctx context.Context, message CachedMessage) error {
	settings, enabled, err := s.loadSettings(ctx, message.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	s.cache.SetGuildLimit(message.GuildID, settings.CacheEntriesPerGuild)
	s.cache.Put(message)
	return nil
}

// Handle delivers one event to its routed channel, filling in cached
// content, and retries up to the guild's limit. A delivered deletion drops
// the message from the cache; a failed one keeps it for a gateway replay.
func (s *Service) Handle(ctx context.Context, event Event) error {
	settings, enabled, err := s.loadSettings(ctx, event.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	channelID := settings.Channels[event.Type]
	if channelID == "" {
		return ErrNoDestination
	}
	s.enrichFromCache(&event)
	payload := formatEvent(event, settings)
	var last error
	for attempt := 1; attempt <= settings.MaxDeliveryAttempts; attempt++ {
		last = s.client.SendStaffLog(ctx, event.GuildID, channelID, payload)
		if last == nil {
			if event.Type == MessageDelete {
				s.cache.Delete(event.GuildID, event.MessageDiscordID)
			}
			s.recordSuccess(event.GuildID)
			return nil
		}
		if attempt == settings.MaxDeliveryAttempts {
			break
		}
		delay := max(time.Duration(attempt)*100*time.Millisecond, retryAfter(last))
		if err := sleep(ctx, delay); err != nil {
			last = err
			break
		}
	}
	s.recordFailure(event.GuildID, last)
	return fmt.Errorf("deliver general log after %d attempts: %w", settings.MaxDeliveryAttempts, last)
}

// HandleBulkDelete logs a bulk deletion with whatever content was cached,
// then drops those messages from the cache.
func (s *Service) HandleBulkDelete(ctx context.Context, guildID, channelID string, messageIDs []string) error {
	var contents []string
	for _, id := range messageIDs {
		if cached, ok := s.cache.Get(guildID, id); ok {
			contents = append(contents, cached.Content)
		}
	}
	err := s.Handle(ctx, Event{
		GuildID:          guildID,
		ChannelDiscordID: channelID,
		Type:             MessageBulkDelete,
		Before:           strings.Join(contents, "\n---\n"),
		Metadata: map[string]string{
			"message_count": strconv.Itoa(len(messageIDs)),
			"cached_count":  strconv.Itoa(len(contents)),
		},
	})
	if err != nil {
		return err
	}
	for _, id := range messageIDs {
		s.cache.Delete(guildID, id)
	}
	return nil
}

// loadSettings returns the guild's settings, defaults if it has none, and
// whether logging is on.
func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	settings, enabled, err := modules.LoadSettings(ctx, s.registry, guildID, modules.GeneralLogging, Defaults())
	if err != nil {
		return Settings{}, false, err
	}
	if settings.Channels == nil {
		settings.Channels = map[EventType]string{}
	}
	return settings, enabled, nil
}

// enrichFromCache fills in what an edit or delete event lacks from the
// cached copy of its message.
func (s *Service) enrichFromCache(event *Event) {
	if event.Type != MessageEdit && event.Type != MessageDelete {
		return
	}
	cached, ok := s.cache.Get(event.GuildID, event.MessageDiscordID)
	if !ok {
		return
	}
	if event.Before == "" {
		event.Before = cached.Content
	}
	if len(event.Attachments) == 0 {
		event.Attachments = cached.Attachments
	}
	if len(event.EmbedTypes) == 0 {
		event.EmbedTypes = cached.EmbedTypes
	}
}

func (s *Service) recordSuccess(guildID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[guildID]
	status.Delivered++
	status.LastError = ""
	status.LastFailureAt = nil
	s.status[guildID] = status
}

func (s *Service) recordFailure(guildID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := s.status[guildID]
	status.Failed++
	status.LastError = err.Error()
	now := time.Now().UTC()
	status.LastFailureAt = &now
	s.status[guildID] = status
}

// audit logs and records a settings operation. Deliveries are never
// audited: general logging is separate from the audit log.
func (s *Service) audit(ctx context.Context, actor modules.Actor, action, result string, cause error) {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	modules.Audit(ctx, s.auditor, "general_logging", modules.AuditEvent{
		GuildID:            actor.GuildID,
		ActorDiscordUserID: actor.DiscordUserID,
		Action:             action,
		ResourceType:       "general_logging_settings",
		Result:             result,
		FailureReason:      reason,
	})
}

// formatEvent renders event as the JSON posted to the log channel,
// including only the message details the guild opted into, with secrets
// redacted.
func formatEvent(event Event, settings Settings) string {
	redact := func(value string) string { return secretPattern.ReplaceAllString(value, "[REDACTED]") }
	payload := map[string]any{
		"event":      event.Type,
		"channel_id": event.ChannelDiscordID,
		"message_id": event.MessageDiscordID,
		"actor_id":   event.ActorDiscordUserID,
	}
	if settings.IncludeMessageContent {
		payload["before"] = redact(event.Before)
		payload["after"] = redact(event.After)
	}
	if settings.IncludeAttachmentMetadata {
		payload["attachments"] = event.Attachments
	}
	if settings.IncludeEmbedMetadata {
		payload["embed_types"] = event.EmbedTypes
	}
	metadata := make(map[string]string, len(event.Metadata))
	for key, value := range event.Metadata {
		metadata[key] = redact(value)
	}
	payload["metadata"] = metadata
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

// destinations returns the distinct channels routes send to, sorted.
func destinations(routes map[EventType]string) []string {
	return slices.Compact(slices.Sorted(maps.Values(routes)))
}

// retryAfter returns how long Discord asked us to wait if err is a rate
// limit, or zero. Delivery turns off discordgo's own rate-limit retries, so
// the limit arrives here as a *discordgo.RateLimitError.
func retryAfter(err error) time.Duration {
	var limited *discordgo.RateLimitError
	if errors.As(err, &limited) && limited.RateLimit != nil && limited.TooManyRequests != nil {
		return limited.RetryAfter
	}
	return 0
}

// sleep waits for d or until ctx is done.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
