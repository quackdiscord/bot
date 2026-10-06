package tickets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// Service holds the ticket rules: who may read and close a ticket, and
// what each change records. Every change is audited. Operations that also
// change Discord go through DiscordAdapter, which calls back in here.
type Service struct {
	registry *modules.Registry
	store    *Store
	auditor  modules.Auditor
	now      func() time.Time
	journal  journal
}

// NewService returns a Service. A nil auditor only logs operations. It
// loads the open tickets' threads so the journal recognizes their messages
// from the first gateway event; if that fails, closing is refused until a
// restart, since messages may have gone unrecorded.
func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor) *Service {
	s := &Service{
		registry: registry,
		store:    store,
		auditor:  auditor,
		now:      func() time.Time { return time.Now().UTC() },
	}
	s.loadJournalThreads()
	return s
}

// Settings returns the guild's ticket settings and whether tickets are on.
// It needs Manage Guild.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, bool, error) {
	if !actor.CanManage {
		return Settings{}, false, ErrPermissionDenied
	}
	return s.loadSettings(ctx, actor.GuildID)
}

// UpdateSettings validates and saves the guild's ticket settings. It needs
// Manage Guild.
func (s *Service) UpdateSettings(ctx context.Context, actor modules.Actor, enabled bool, settings Settings) (Settings, error) {
	const action = "ticket.settings.update"
	if !actor.CanManage {
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

// RecordEntryPanel saves where setup posted the entry panel. It is
// bookkeeping for the setup already audited, so it records no audit entry.
func (s *Service) RecordEntryPanel(ctx context.Context, actor modules.Actor, channelID, messageID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	if channelID == "" || messageID == "" {
		return errors.New("entry panel receipt is incomplete")
	}
	return s.store.saveEntryPanel(ctx, actor.GuildID, channelID, messageID)
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

// ActiveForMember returns the actor's own ticket that holds their slot,
// open or still closing. It is nil while an opening is only reserved.
func (s *Service) ActiveForMember(ctx context.Context, actor modules.Actor) (*Ticket, error) {
	if actor.GuildID == "" || actor.DiscordUserID == "" {
		return nil, ErrPermissionDenied
	}
	return s.store.activeForMember(ctx, actor.GuildID, actor.DiscordUserID)
}

// ClosurePending reports whether a closed ticket still holds its owner's
// slot because its close stopped before the thread was deleted.
func (s *Service) ClosurePending(ctx context.Context, actor modules.Actor, ticketID string) (bool, error) {
	ticket, err := s.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return false, err
	}
	if ticket.Status != StatusResolved {
		return false, nil
	}
	active, err := s.store.activeForMember(ctx, ticket.GuildID, ticket.OwnerDiscordUserID)
	return active != nil && active.ID == ticket.ID, err
}

// Resolve closes an open ticket with transcript as its saved transcript.
// The owner and moderators may close, even with tickets switched off. The
// owner's slot stays held until the adapter has deleted the thread.
func (s *Service) Resolve(ctx context.Context, actor modules.Actor, ticketID, transcript string) (*Ticket, error) {
	if _, err := s.visibleTicket(ctx, actor, ticketID); err != nil {
		return nil, err
	}
	settings, _, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	saved := Transcript{
		TicketID:   ticketID,
		GuildID:    actor.GuildID,
		Content:    transcript,
		CapturedAt: now,
		ExpiresAt:  now.AddDate(0, 0, settings.TranscriptRetentionDays),
	}
	ticket, err := s.store.captureClosure(ctx, actor.GuildID, ticketID, actor.DiscordUserID, saved, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "failure", err)
		return nil, err
	}
	s.forgetJournalThread(ticket.ThreadDiscordChannelID)
	s.audit(ctx, actor, "ticket.resolve", ticket.ID, "success", nil)
	return ticket, nil
}

// RecordChannelMissing notes on a ticket's timeline that its thread was
// deleted. The ticket stays as it was for staff to deal with.
func (s *Service) RecordChannelMissing(ctx context.Context, guildID, ticketID, channelID string) error {
	return s.recordSystemEvent(ctx, guildID, ticketID, EventChannelMissing,
		"Private ticket channel was deleted", fmt.Sprintf(`{"channel_id":%q}`, channelID))
}

// RecordPermissionsRepaired notes on a ticket's timeline that its access
// was repaired.
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

// PurgeExpiredTranscripts deletes transcripts and journaled message text
// past their retention and returns how many transcripts went. Timelines are
// kept.
func (s *Service) PurgeExpiredTranscripts(ctx context.Context) (int64, error) {
	return s.store.purgeExpired(ctx, s.now())
}

// visibleTicket returns a ticket the actor may read: their own, or any
// ticket for a moderator.
func (s *Service) visibleTicket(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if !actor.CanModerate && actor.DiscordUserID != ticket.OwnerDiscordUserID {
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
	return s.store.append(ctx, ticket, eventType, modules.SystemActorID, body, metadata, s.now())
}

// loadSettings returns the guild's settings, defaults if it has none, and
// whether tickets are on.
func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	return modules.LoadSettings(ctx, s.registry, guildID, modules.Tickets, Defaults())
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
