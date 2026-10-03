package tickets

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// DiscordClient is what the DiscordAdapter needs from Discord. The module's
// channels type implements it; tests use a fake.
type DiscordClient interface {
	CreatePrivateTicketChannel(context.Context, string, string, Settings) (string, error)
	EnsureTicketPermissions(context.Context, string, string, string, []string) error
	SendTicketReply(context.Context, string, string) error
	CaptureTicketTranscript(context.Context, string) (string, error)
	ArchiveTicketChannel(context.Context, string) error
	DeleteProvisionalTicketChannel(context.Context, string) error
}

// DiscordAdapter runs the ticket operations that also change Discord:
// opening creates a private channel, closing archives it, and so on. The
// Service holds the rules; the adapter orders the Discord calls around them.
type DiscordAdapter struct {
	service *Service
	client  DiscordClient
}

// NewDiscordAdapter returns a DiscordAdapter over service and client.
func NewDiscordAdapter(service *Service, client DiscordClient) *DiscordAdapter {
	return &DiscordAdapter{service: service, client: client}
}

// Open reserves the member's ticket slot, creates and locks down a private
// channel, and only then commits the ticket. The reservation comes first so
// repeated clicks cannot create a pile of channels.
func (a *DiscordAdapter) Open(ctx context.Context, actor modules.Actor) (*Ticket, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	settings, enabled, err := a.service.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrDisabled
	}
	token, err := a.service.store.reserveOpening(ctx, actor, settings.DailyOpenLimit, a.service.now())
	if err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cleanupCancel()
		if err := a.service.store.releaseOpening(cleanupCtx, actor, token); err != nil {
			slog.ErrorContext(cleanupCtx, "Ticket opening reservation cleanup failed", "guild_id", actor.GuildID, "ticket_id", token)
		}
	}()
	channelID, err := a.client.CreatePrivateTicketChannel(ctx, actor.GuildID, actor.DiscordUserID, settings)
	if err != nil {
		return nil, err
	}
	if err := a.client.EnsureTicketPermissions(ctx, channelID, actor.DiscordUserID, actor.GuildID, settings.StaffRoleDiscordIDs); err != nil {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cleanupCancel()
		_ = a.client.DeleteProvisionalTicketChannel(cleanupCtx, channelID)
		return nil, err
	}
	ticket, err := a.service.store.finishOpening(ctx, actor, token, channelID, a.service.now())
	if err != nil {
		// A failed commit acknowledgement may conceal a committed ticket. Keep
		// its private channel instead of deleting a potentially accepted ticket.
		a.service.audit(ctx, actor, "ticket.open", token, "failure", err)
		return nil, err
	}
	a.service.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	return ticket, nil
}

// Reply posts body in the ticket and records it, once the actor is
// authorized.
func (a *DiscordAdapter) Reply(ctx context.Context, actor modules.Actor, ticketID, body string) error {
	if err := validateReply(body); err != nil {
		return err
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if err := a.client.SendTicketReply(ctx, ticket.ThreadDiscordChannelID, body); err != nil {
		return err
	}
	return a.service.Reply(ctx, actor, ticketID, body)
}

// Close saves the transcript, resolves the ticket, and archives its channel.
// Retrying after an archive failure only retries the archive.
func (a *DiscordAdapter) Close(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	resolved := ticket
	if ticket.Status == StatusOpen {
		transcript, err := a.client.CaptureTicketTranscript(ctx, ticket.ThreadDiscordChannelID)
		if err != nil {
			return nil, err
		}
		resolved, err = a.service.Resolve(ctx, actor, ticketID, transcript)
		if err != nil {
			return nil, err
		}
	} else if ticket.Status != StatusResolved {
		return nil, ErrInvalidTransition
	}
	if err := a.client.ArchiveTicketChannel(ctx, ticket.ThreadDiscordChannelID); err != nil {
		return resolved, err
	}
	return resolved, nil
}

// Cancel is Close for a cancellation by the owner or staff.
func (a *DiscordAdapter) Cancel(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	cancelled := ticket
	if ticket.Status == StatusOpen {
		transcript, err := a.client.CaptureTicketTranscript(ctx, ticket.ThreadDiscordChannelID)
		if err != nil {
			return nil, err
		}
		cancelled, err = a.service.cancel(ctx, actor, ticketID, &transcript)
		if err != nil {
			return nil, err
		}
	} else if ticket.Status != StatusCancelled {
		return nil, ErrInvalidTransition
	}
	if err := a.client.ArchiveTicketChannel(ctx, ticket.ThreadDiscordChannelID); err != nil {
		return cancelled, err
	}
	return cancelled, nil
}

// RepairPermissions resets the ticket's ACL to owner, staff, and bot, and
// records the repair. It needs Manage Guild.
func (a *DiscordAdapter) RepairPermissions(ctx context.Context, actor modules.Actor, ticketID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	ticket, _, err := a.service.Detail(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	settings, enabled, err := a.service.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	if err := a.client.EnsureTicketPermissions(ctx, ticket.ThreadDiscordChannelID, ticket.OwnerDiscordUserID, actor.GuildID, settings.StaffRoleDiscordIDs); err != nil {
		return err
	}
	return a.service.RecordPermissionsRepaired(ctx, actor.GuildID, ticketID)
}

// HandleDeletedChannel records on a ticket's timeline that its channel was
// deleted. The ticket itself is left as it was.
func (a *DiscordAdapter) HandleDeletedChannel(ctx context.Context, guildID, ticketID, channelID string) error {
	if a == nil || a.service == nil {
		return errors.New("ticket Discord adapter is not configured")
	}
	return a.service.RecordChannelMissing(ctx, guildID, ticketID, channelID)
}

// HandleDeletedEntryChannel turns tickets off if channelID was the entry
// channel, until an admin picks a new one.
func (a *DiscordAdapter) HandleDeletedEntryChannel(ctx context.Context, guildID, channelID string) error {
	return a.service.RepairDeletedEntryChannel(ctx, guildID, channelID)
}
