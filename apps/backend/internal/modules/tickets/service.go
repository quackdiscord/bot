package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// Service is the ticket lifecycle: open, reply, resolve, cancel, reopen,
// with each operation authorized against the caller's actor and audited.
type Service struct {
	registry *modules.Registry
	store    *Store
	auditor  modules.Auditor
	now      func() time.Time
}

// NewService returns a Service. A nil auditor only logs operations.
func NewService(registry *modules.Registry, store *Store, auditor modules.Auditor) *Service {
	return &Service{registry: registry, store: store, auditor: auditor, now: func() time.Time { return time.Now().UTC() }}
}

// Settings returns one guild's ticket settings to current managers.
func (s *Service) Settings(ctx context.Context, actor modules.Actor) (Settings, bool, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.read", "", "denied", ErrPermissionDenied)
		return Settings{}, false, ErrPermissionDenied
	}
	return s.loadSettings(ctx, actor.GuildID)
}

// Status returns non-content ticket health to current staff.
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
	return ModuleStatus{Enabled: enabled, EntryConfigured: strings.TrimSpace(settings.EntryChannelDiscordID) != "", OpenTickets: open}, nil
}

// UpdateSettings validates and replaces one guild's ticket configuration.
func (s *Service) UpdateSettings(ctx context.Context, actor modules.Actor, enabled bool, settings Settings) (Settings, error) {
	if !actor.CanManage {
		s.audit(ctx, actor, "ticket.settings.update", "", "denied", ErrPermissionDenied)
		return Settings{}, ErrPermissionDenied
	}
	if err := validateSettings(settings, enabled); err != nil {
		s.audit(ctx, actor, "ticket.settings.update", "", "failure", err)
		return Settings{}, err
	}
	payload, _ := json.Marshal(settings)
	configuration, err := s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: actor.GuildID, ModuleID: modules.Tickets, Enabled: enabled, ConfigJSON: string(payload)})
	if err != nil {
		s.audit(ctx, actor, "ticket.settings.update", "", "failure", err)
		return Settings{}, err
	}
	s.audit(ctx, actor, "ticket.settings.update", configuration.ID, "success", nil)
	return settings, nil
}

// Open creates one member ticket after duplicate and rolling-day limits pass.
func (s *Service) Open(ctx context.Context, actor modules.Actor, threadDiscordChannelID string) (*Ticket, error) {
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	if strings.TrimSpace(actor.DiscordUserID) == "" || strings.TrimSpace(threadDiscordChannelID) == "" {
		return nil, errors.New("member and private channel are required")
	}
	ticket, err := s.store.create(ctx, actor.GuildID, actor.DiscordUserID, threadDiscordChannelID, settings.DailyOpenLimit, s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.open", "", "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	return ticket, nil
}

// Resolve closes an open ticket as current staff and stores its captured transcript.
func (s *Service) Resolve(ctx context.Context, actor modules.Actor, ticketID, transcript string) (*Ticket, error) {
	if !actor.CanModerate {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	now := s.now()
	transcriptRecord := &Transcript{TicketID: ticketID, GuildID: actor.GuildID, Content: transcript, CapturedAt: now, ExpiresAt: now.AddDate(0, 0, settings.TranscriptRetentionDays)}
	ticket, err := s.store.transition(ctx, actor.GuildID, ticketID, []Status{StatusOpen}, StatusResolved, actor.DiscordUserID, EventResolved, "Ticket resolved", true, transcriptRecord, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.resolve", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.resolve", ticket.ID, "success", nil)
	return ticket, nil
}

// Cancel closes an open ticket at the owner's request or by current staff.
func (s *Service) Cancel(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	return s.cancel(ctx, actor, ticketID, nil)
}

// cancel cancels a ticket, saving transcriptContent when the caller
// captured one.
func (s *Service) cancel(ctx context.Context, actor modules.Actor, ticketID string, transcriptContent *string) (*Ticket, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, _, settingsErr := s.loadSettings(ctx, actor.GuildID)
	now := s.now()
	var transcript *Transcript
	if settingsErr == nil && transcriptContent != nil {
		transcript = &Transcript{TicketID: ticketID, GuildID: actor.GuildID, Content: *transcriptContent, CapturedAt: now, ExpiresAt: now.AddDate(0, 0, settings.TranscriptRetentionDays)}
	}
	ticket, err = s.store.transition(ctx, actor.GuildID, ticketID, []Status{StatusOpen}, StatusCancelled, actor.DiscordUserID, EventCancelled, "Ticket cancelled", false, transcript, now)
	if err != nil {
		s.audit(ctx, actor, "ticket.cancel", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.cancel", ticket.ID, "success", nil)
	return ticket, nil
}

// Reopen restores a recently resolved or cancelled ticket for current staff.
func (s *Service) Reopen(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	if !actor.CanModerate {
		s.audit(ctx, actor, "ticket.reopen", ticketID, "denied", ErrPermissionDenied)
		return nil, ErrPermissionDenied
	}
	settings, enabled, err := s.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	current, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if s.now().Sub(current.UpdatedAt) > time.Duration(settings.ReopenWindowHours)*time.Hour {
		return nil, ErrInvalidTransition
	}
	ticket, err := s.store.transition(ctx, actor.GuildID, ticketID, []Status{StatusResolved, StatusCancelled}, StatusOpen, actor.DiscordUserID, EventReopened, "Ticket reopened", false, nil, s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.reopen", ticketID, "failure", err)
		return nil, err
	}
	s.audit(ctx, actor, "ticket.reopen", ticket.ID, "success", nil)
	return ticket, nil
}

// Reply records a private reply timeline event after owner-or-staff authorization.
func (s *Service) Reply(ctx context.Context, actor modules.Actor, ticketID, body string) error {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusOpen {
		return ErrInvalidTransition
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return ErrPermissionDenied
	}
	if err := validateReply(body); err != nil {
		return err
	}
	err = s.store.append(ctx, *ticket, EventReplied, actor.DiscordUserID, body, "{}", s.now())
	if err != nil {
		s.audit(ctx, actor, "ticket.reply", ticketID, "failure", err)
		return err
	}
	s.audit(ctx, actor, "ticket.reply", ticketID, "success", nil)
	return nil
}

// validateReply bounds a reply to Discord's message length.
func validateReply(body string) error {
	if len(strings.TrimSpace(body)) == 0 || len(body) > 4000 {
		return errors.New("ticket reply must contain 1 to 4000 characters")
	}
	return nil
}

// Queue lists guild tickets for current staff.
func (s *Service) Queue(ctx context.Context, actor modules.Actor, status Status, limit int) ([]Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.list(ctx, actor.GuildID, status, limit)
}

// Detail returns a private ticket and timeline to its owner or current staff.
func (s *Service) Detail(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, []Event, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return nil, nil, ErrPermissionDenied
	}
	events, err := s.store.timeline(ctx, actor.GuildID, ticketID)
	return ticket, events, err
}

// Transcript returns retained private content to its owner or current staff.
func (s *Service) Transcript(ctx context.Context, actor modules.Actor, ticketID string) (*Transcript, error) {
	ticket, err := s.store.get(ctx, actor.GuildID, ticketID)
	if err != nil {
		return nil, err
	}
	if actor.DiscordUserID != ticket.OwnerDiscordUserID && !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return s.store.transcript(ctx, actor.GuildID, ticketID, s.now())
}

// RecordChannelMissing notes on a ticket's timeline that its channel was
// deleted. The ticket stays as it was for staff to deal with.
func (s *Service) RecordChannelMissing(ctx context.Context, guildID, ticketID, channelID string) error {
	ticket, err := s.store.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	return s.store.append(ctx, *ticket, EventChannelMissing, "quack-system", "Private ticket channel was deleted", fmt.Sprintf(`{"channel_id":%q}`, channelID), s.now())
}

// RepairDeletedEntryChannel turns tickets off and clears the entry channel
// if channelID was it.
func (s *Service) RepairDeletedEntryChannel(ctx context.Context, guildID, channelID string) error {
	settings, _, err := s.loadSettings(ctx, guildID)
	if err != nil {
		return err
	}
	if settings.EntryChannelDiscordID != channelID {
		return nil
	}
	settings.EntryChannelDiscordID = ""
	payload, _ := json.Marshal(settings)
	configuration, err := s.registry.SetConfiguration(ctx, modules.Configuration{GuildID: guildID, ModuleID: modules.Tickets, Enabled: false, ConfigJSON: string(payload)})
	if err != nil {
		return err
	}
	s.audit(ctx, modules.Actor{GuildID: guildID, DiscordUserID: "quack-system"}, "ticket.entry_channel_repair", configuration.ID, "success", nil)
	return nil
}

// PurgeExpiredTranscripts deletes transcripts past their retention and
// returns how many. Timelines are kept.
func (s *Service) PurgeExpiredTranscripts(ctx context.Context) (int64, error) {
	return s.store.purgeExpiredTranscripts(ctx, s.now())
}

// RecordPermissionsRepaired notes on a ticket's timeline that its ACL was
// repaired.
func (s *Service) RecordPermissionsRepaired(ctx context.Context, guildID, ticketID string) error {
	ticket, err := s.store.get(ctx, guildID, ticketID)
	if err != nil {
		return err
	}
	return s.store.append(ctx, *ticket, EventPermissionsRepaired, "quack-system", "Private ticket permissions repaired", "{}", s.now())
}

// loadSettings returns the guild's settings, defaults if it has none, and
// whether tickets are on.
func (s *Service) loadSettings(ctx context.Context, guildID string) (Settings, bool, error) {
	configuration, err := s.registry.Configuration(ctx, guildID, modules.Tickets)
	if err != nil {
		return Settings{}, false, err
	}
	if configuration == nil {
		return Defaults(), false, nil
	}
	settings := Defaults()
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil {
		return Settings{}, false, err
	}
	return settings, configuration.Enabled, nil
}

// validateSettings checks settings; an enabled module also needs an entry
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

// audit logs and records a ticket operation.
func (s *Service) audit(ctx context.Context, actor modules.Actor, action, resourceID, result string, operationErr error) {
	reason := ""
	if operationErr != nil {
		reason = operationErr.Error()
	}
	modules.Audit(ctx, s.auditor, "tickets", modules.AuditEvent{
		GuildID: actor.GuildID, ActorDiscordUserID: actor.DiscordUserID,
		Action: action, ResourceType: "ticket", ResourceID: resourceID,
		Result: result, FailureReason: reason,
	})
}
