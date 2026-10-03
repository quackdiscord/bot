package tickets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// Service holds the ticket rules: who may read, reply to, resolve, cancel,
// or reopen a ticket, and when. Every change is audited. Operations that
// also change Discord go through DiscordAdapter, which calls back in here.
type Service struct {
	registry *modules.Registry
	store    *Store
	auditor  modules.Auditor
	now      func() time.Time
}

// NewService returns a Service. A nil auditor only logs operations.
func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor) *Service {
	return &Service{
		registry: registry,
		store:    store,
		auditor:  auditor,
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// Settings returns the guild's ticket settings and whether tickets are on.
// It needs Manage Guild.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, bool, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.read", "", "denied", ErrPermissionDenied)
		return Settings{}, false, ErrPermissionDenied
	}
	return s.loadSettings(ctx, actor.GuildID)
}

// UpdateSettings validates and saves the guild's ticket settings. It needs
// Manage Guild.
func (s *Service) UpdateSettings(ctx context.Context, actor modules.Actor, enabled bool, settings Settings) (Settings, error) {
	const action = "ticket.settings.update"
	if !actor.CanManage {
		s.audit(ctx, actor, action, "", "denied", ErrPermissionDenied)
		return Settings{}, ErrPermissionDenied
	}
	if err := validateSettings(settings, enabled); err != nil {
		s.audit(ctx, actor, action, "", "failure", err)
		return Settings{}, err
	}
	configuration, err := s.registry.SaveSettings(ctx, actor.GuildID, modules.Tickets, enabled, settings)
	if err != nil {
		s.audit(ctx, actor, action, "", "failure", err)
		return Settings{}, err
	}
	s.audit(ctx, actor, action, configuration.ID, "success", nil)
	return settings, nil
}

// Status returns a content-free summary of the guild's tickets to staff.
func (s *Service) Status(ctx context.Context, actor modules.Actor) (ModuleStatus, error) {
	if !actor.CanManage && !actor.CanModerate {
		return ModuleStatus{}, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return ModuleStatus{}, err
	}
	open, err := s.store.count(ctx, actor.GuildID, StatusOpen)
	if err != nil {
		return ModuleStatus{}, err
	}
	return ModuleStatus{
		Enabled:         enabled,
		EntryConfigured: strings.TrimSpace(settings.EntryChannelDiscordID) != "",
		OpenTickets:     open,
	}, nil
}

// Queue lists the guild's tickets, optionally only those in status, to
// moderators.
func (s *Service) Queue(ctx context.Context, actor modules.Actor, status Status, limit int) ([]Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.list(ctx, actor.GuildID, status, limit)
}

// Detail returns a ticket and its timeline to its owner or a moderator.
func (s *Service) Detail(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, []Event, error) {
	ticket, err := s.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, nil, err
	}
	events, err := s.store.timeline(ctx, actor.GuildID, ticketID)
	return ticket, events, err
}

// Transcript returns a closed ticket's retained transcript to its owner or
// a moderator.
func (s *Service) Transcript(ctx context.Context, actor modules.Actor, ticketID string) (*Transcript, error) {
	if _, err := s.visibleTicket(ctx, actor, ticketID); err != nil {
		return nil, err
	}
	return s.store.transcript(ctx, actor.GuildID, ticketID, s.now())
}

// Reply records a reply on an open ticket's timeline. The owner and
// moderators may reply.
func (s *Service) Reply(ctx context.Context, actor modules.Actor, ticketID, body string) error {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusOpen {
		return ErrInvalidTransition
	}
	if !canAccess(actor, ticket) {
		return ErrPermissionDenied
	}
	if err := validateReply(body); err != nil {
		return err
	}
	err = s.store.append(ctx, Event{
		TicketID:           ticket.ID,
		GuildID:            ticket.GuildID,
		Type:               EventReplied,
		ActorDiscordUserID: actor.DiscordUserID,
		Body:               body,
		MetadataJSON:       "{}",
		CreatedAt:          s.now(),
	})
	if err != nil {
		s.audit(ctx, actor, "ticket.reply", ticketID, "failure", err)
		return err
	}
	s.audit(ctx, actor, "ticket.reply", ticketID, "success", nil)
	return nil
}

// Resolve closes an open ticket as done and saves transcript. It needs a
// moderator.
func (s *Service) Resolve(ctx context.Context, actor modules.Actor, ticketID, transcript string) (*Ticket, error) {
	if !actor.CanModerate {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, err := s.enabledSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	saved := newTranscript(actor.GuildID, ticketID, transcript, settings, now)
	ticket, err := s.store.transition(ctx, actor.GuildID, ticketID, StatusResolved, actor.DiscordUserID, saved, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.resolve", ticket.ID, "success", nil)
	return ticket, nil
}

// Cancel withdraws an open ticket without saving a transcript. The owner
// and moderators may cancel.
func (s *Service) Cancel(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	return s.cancel(ctx, actor, ticketID, nil)
}

// Reopen returns a resolved or cancelled ticket to open, within the guild's
// reopen window. It needs a moderator.
func (s *Service) Reopen(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	if !actor.CanModerate {
		s.audit(ctx, actor, "ticket.reopen", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, err := s.enabledSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if s.now().Sub(current.UpdatedAt) > time.Duration(settings.ReopenWindowHours)*time.Hour {
		return nil, ErrInvalidTransition
	}
	ticket, err := s.store.transition(ctx, actor.GuildID, ticketID, StatusOpen, actor.DiscordUserID, nil, s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.reopen", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.reopen", ticket.ID, "success", nil)
	return ticket, nil
}

// RecordChannelMissing notes on a ticket's timeline that its channel was
// deleted. The ticket stays as it was for staff to deal with.
func (s *Service) RecordChannelMissing(ctx context.Context, guildID, ticketID, channelID string) error {
	return s.recordSystemEvent(ctx, guildID, ticketID, EventChannelMissing,
		"Private ticket channel was deleted", fmt.Sprintf(`{"channel_id":%q}`, channelID))
}

// RecordPermissionsRepaired notes on a ticket's timeline that its ACL was
// repaired.
func (s *Service) RecordPermissionsRepaired(ctx context.Context, guildID, ticketID string) error {
	return s.recordSystemEvent(ctx, guildID, ticketID, EventPermissionsRepaired,
		"Private ticket permissions repaired", "{}")
}

// RepairDeletedEntryChannel turns tickets off and clears the entry channel
// if channelID was it, until an admin picks a new one.
func (s *Service) RepairDeletedEntryChannel(ctx context.Context, guildID, channelID string) error {
	settings, _, err := s.loadSettings(ctx, guildID)
	if err != nil {
		return err
	}
	if settings.EntryChannelDiscordID != channelID {
		return nil
	}
	settings.EntryChannelDiscordID = ""
	configuration, err := s.registry.SaveSettings(ctx, guildID, modules.Tickets, false, settings)
	if err != nil {
		return err
	}
	system := modules.Actor{GuildID: guildID, DiscordUserID: modules.SystemActorID}
	s.audit(ctx, system, "ticket.entry_channel_repair", configuration.ID, "success", nil)
	return nil
}

// PurgeExpiredTranscripts deletes transcripts past their retention and
// returns how many. Timelines are kept.
func (s *Service) PurgeExpiredTranscripts(ctx context.Context) (int64, error) {
	return s.store.purgeExpiredTranscripts(ctx, s.now())
}

// cancel cancels an open ticket, saving transcript when the caller captured
// one. A transcript is dropped, not fatal, if the settings cannot be read.
func (s *Service) cancel(ctx context.Context, actor modules.Actor, ticketID string, transcript *string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if !canAccess(actor, ticket) {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	now := s.now()
	var saved *Transcript
	if settings, _, err := s.loadSettings(ctx, actor.GuildID); err == nil && transcript != nil {
		saved = newTranscript(actor.GuildID, ticketID, *transcript, settings, now)
	}
	ticket, err = s.store.transition(ctx, actor.GuildID, ticketID, StatusCancelled, actor.DiscordUserID, saved, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.cancel", ticket.ID, "success", nil)
	return ticket, nil
}

// visibleTicket returns a ticket the actor may read: their own, or any
// ticket for a moderator.
func (s *Service) visibleTicket(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if !canAccess(actor, ticket) {
		return nil, ErrPermissionDenied
	}
	return ticket, nil
}

// recordSystemEvent adds an entry Quack itself made to an existing ticket's
// timeline.
func (s *Service) recordSystemEvent(ctx context.Context, guildID, ticketID string, eventType EventType, body, metadata string) error {
	ticket, err := s.store.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	return s.store.append(ctx, Event{
		TicketID:           ticket.ID,
		GuildID:            ticket.GuildID,
		Type:               eventType,
		ActorDiscordUserID: modules.SystemActorID,
		Body:               body,
		MetadataJSON:       metadata,
		CreatedAt:          s.now(),
	})
}

// loadSettings returns the guild's settings, defaults if it has none, and
// whether tickets are on.
func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	return modules.LoadSettings(ctx, s.registry, guildID, modules.Tickets, Defaults())
}

// enabledSettings returns the guild's settings, or ErrDisabled if tickets
// are off.
func (s *Service) enabledSettings(ctx context.Context, guildID string) (Settings, error) {
	settings, enabled, err := s.loadSettings(ctx, guildID)
	if err != nil {
		return Settings{}, err
	}
	if !enabled {
		return Settings{}, ErrDisabled
	}
	return settings, nil
}

// audit logs and records a ticket operation.
func (s *Service) audit(ctx context.Context, actor modules.Actor, action, resourceID, result string, cause error) {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	modules.Audit(ctx, s.auditor, "tickets", modules.AuditEvent{
		GuildID:            actor.GuildID,
		ActorDiscordUserID: actor.DiscordUserID,
		Action:             action,
		ResourceType:       "ticket",
		ResourceID:         resourceID,
		Result:             result,
		FailureReason:      reason,
	})
}

// canAccess reports whether actor may read and act on ticket: its owner and
// every moderator may.
func canAccess(actor modules.Actor, ticket *Ticket) bool {
	return actor.CanModerate || actor.DiscordUserID == ticket.OwnerDiscordUserID
}

// newTranscript stamps a captured transcript with the guild's retention.
func newTranscript(guildID, ticketID, content string, settings Settings, now time.Time) *Transcript {
	return &Transcript{
		TicketID:   ticketID,
		GuildID:    guildID,
		Content:    content,
		CapturedAt: now,
		ExpiresAt:  now.AddDate(0, 0, settings.TranscriptRetentionDays),
	}
}
