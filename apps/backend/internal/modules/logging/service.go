package logging

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// secretPattern matches bot tokens, webhook URLs, and key=value secrets that
// members paste into chat, so they are redacted before reaching a log
// channel.
var secretPattern = regexp.MustCompile(`(?i)(bot\s+[A-Za-z0-9._-]{20,}|https://(?:discord(?:app)?\.com/api/)?webhooks/[^\s]+|(?:token|secret|authorization)\s*[:=]\s*[^\s]+)`)

// maxBulkMessages is the most messages Discord deletes in one bulk delete.
const maxBulkMessages = 100

// DeliveryClient posts log messages. Messages are already redacted.
type DeliveryClient interface {
	SendStaffLog(ctx context.Context, guildID, channelID string, message discord.Message) error
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

// Service renders, redacts, and delivers events with bounded retries, and
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
	if _, err := s.enabledSettings(ctx, message.GuildID); err != nil {
		return err
	}
	s.cache.Put(message)
	return nil
}

// PrepareMessageEdit caches current and returns the edit event to queue,
// with the message as it was before frozen into it, so a queued edit never
// reads a newer cached copy. before is Discord's own copy, when it sent
// one; otherwise the cache's is used. It returns nil when nothing a member
// would notice changed, such as Discord refreshing a file's signed URL.
func (s *Service) PrepareMessageEdit(ctx context.Context, current CachedMessage, before *CachedMessage) (*Event, error) {
	if _, err := s.enabledSettings(ctx, current.GuildID); err != nil {
		return nil, err
	}
	previous, known := s.cache.Replace(current)
	if before != nil {
		previous, known = *before, true
	}
	if known && previous.Content == current.Content &&
		slices.EqualFunc(previous.Attachments, current.Attachments, sameFile) {
		return nil, nil
	}
	actor := current.AuthorDiscordUserID
	if actor == "" {
		actor = previous.AuthorDiscordUserID
	}
	return &Event{
		GuildID:            current.GuildID,
		ChannelDiscordID:   current.ChannelDiscordID,
		MessageDiscordID:   current.MessageDiscordID,
		ActorDiscordUserID: actor,
		Type:               MessageEdit,
		Before:             previous.Content,
		After:              current.Content,
		Attachments:        current.Attachments,
		EmbedTypes:         current.EmbedTypes,
		SnapshotComplete:   true,
		BeforeKnown:        known,
		BeforeAttachments:  previous.Attachments,
	}, nil
}

// Handle delivers one event to its routed channel, filling in cached
// content, and retries up to the guild's limit. A delivered deletion drops
// the message from the cache; a failed one keeps it for a gateway replay.
func (s *Service) Handle(ctx context.Context, event Event) error {
	settings, err := s.enabledSettings(ctx, event.GuildID)
	if err != nil {
		return err
	}
	channelID := settings.Channels[event.Type]
	if channelID == "" {
		return ErrNoDestination
	}
	s.enrichFromCache(&event)
	message := logMessage(present(event, settings))
	var last error
	for attempt := 1; attempt <= settings.MaxDeliveryAttempts; attempt++ {
		last = s.client.SendStaffLog(ctx, event.GuildID, channelID, message)
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

// HandleBulkDelete logs a bulk deletion with each cached message, then
// drops those messages from the cache.
func (s *Service) HandleBulkDelete(ctx context.Context, guildID, channelID string, messageIDs []string) error {
	if len(messageIDs) > maxBulkMessages {
		return fmt.Errorf("bulk delete of %d messages exceeds Discord's %d", len(messageIDs), maxBulkMessages)
	}
	var cached []CachedMessage
	for _, id := range messageIDs {
		if message, ok := s.cache.Get(guildID, id); ok {
			cached = append(cached, message)
		}
	}
	err := s.Handle(ctx, Event{
		GuildID:          guildID,
		ChannelDiscordID: channelID,
		Type:             MessageBulkDelete,
		BulkMessages:     cached,
		Metadata: map[string]string{
			"message_count": strconv.Itoa(len(messageIDs)),
			"cached_count":  strconv.Itoa(len(cached)),
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

// enabledSettings returns the settings of a guild with logging on, applying
// its cache limit, or ErrDisabled.
func (s *Service) enabledSettings(ctx context.Context, guildID string) (Settings, error) {
	settings, enabled, err := s.loadSettings(ctx, guildID)
	if err != nil {
		return Settings{}, err
	}
	if !enabled {
		return Settings{}, ErrDisabled
	}
	s.cache.SetGuildLimit(guildID, settings.CacheEntriesPerGuild)
	return settings, nil
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
// cached copy of its message. A prepared edit already has its snapshot.
func (s *Service) enrichFromCache(event *Event) {
	if event.SnapshotComplete || (event.Type != MessageEdit && event.Type != MessageDelete) {
		return
	}
	cached, ok := s.cache.Get(event.GuildID, event.MessageDiscordID)
	if !ok {
		return
	}
	if event.ActorDiscordUserID == "" {
		event.ActorDiscordUserID = cached.AuthorDiscordUserID
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

// present reduces event to what the guild opted to show, with secrets
// redacted.
func present(event Event, settings Settings) entry {
	redact := func(value string) string { return secretPattern.ReplaceAllString(value, "[REDACTED]") }
	e := entry{
		Type:      event.Type,
		ChannelID: event.ChannelDiscordID,
		MessageID: event.MessageDiscordID,
		ActorID:   event.ActorDiscordUserID,
		Metadata:  make(map[string]string, len(event.Metadata)),
	}
	if settings.IncludeMessageContent {
		e.Before, e.After = redact(event.Before), redact(event.After)
		if event.SnapshotComplete {
			e.BeforeKnown = &event.BeforeKnown
		}
	}
	if settings.IncludeAttachmentMetadata {
		e.Attachments, e.BeforeAttachments = event.Attachments, event.BeforeAttachments
	}
	if settings.IncludeEmbedMetadata {
		e.EmbedTypes = event.EmbedTypes
	}
	for _, cached := range event.BulkMessages {
		m := entryMessage{MessageID: cached.MessageDiscordID, ActorID: cached.AuthorDiscordUserID}
		if settings.IncludeMessageContent {
			m.Content = redact(cached.Content)
		}
		if settings.IncludeAttachmentMetadata {
			m.Attachments = cached.Attachments
		}
		if settings.IncludeEmbedMetadata {
			m.EmbedTypes = cached.EmbedTypes
		}
		e.Messages = append(e.Messages, m)
	}
	for key, value := range event.Metadata {
		e.Metadata[key] = redact(value)
	}
	return e
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
