package tickets

import (
	"context"
	"log/slog"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// DiscordClient is what DiscordAdapter needs from Discord. guildID is always
// Quack's internal guild ID. The module's channels type implements it; tests
// use a fake.
type DiscordClient interface {
	// CreateChannel creates a new ticket's private thread or channel and
	// returns its ID.
	CreateChannel(ctx context.Context, guildID, ownerID string, settings Settings) (string, error)
	// EnsureAccess makes a ticket visible to exactly its owner, current
	// staff, and the bot.
	EnsureAccess(ctx context.Context, guildID, channelID, ownerID string, staffRoleIDs []string) error
	// SendReply posts a reply in a ticket.
	SendReply(ctx context.Context, channelID, body string) error
	// CaptureTranscript renders a ticket's message history.
	CaptureTranscript(ctx context.Context, channelID string) (string, error)
	// ArchiveChannel closes a ticket's channel to further messages.
	ArchiveChannel(ctx context.Context, channelID string) error
	// DeleteChannel deletes a channel created for a ticket that was never
	// committed.
	DeleteChannel(ctx context.Context, channelID string) error
}

// DiscordAdapter runs the ticket operations that also change Discord:
// opening creates a private channel, closing archives it, and so on. The
// Service holds the rules; the adapter orders the Discord calls around them
// so that a failure part way leaves nothing a retry cannot finish.
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
	settings, err := a.service.enabledSettings(ctx, actor.GuildID)
	if err != nil {
		return nil, err
	}
	store := a.service.store
	token, err := store.reserveOpening(ctx, actor, settings.DailyOpenLimit, a.service.now())
	if err != nil {
		return nil, err
	}
	// Release the slot if the ticket never commits. Once it has, this is a
	// no-op.
	defer func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := store.releaseOpening(ctx, actor, token); err != nil {
			slog.ErrorContext(ctx, "Ticket opening reservation cleanup failed",
				"guild_id", actor.GuildID, "ticket_id", token, "error", err)
		}
	}()
	channelID, err := a.client.CreateChannel(ctx, actor.GuildID, actor.DiscordUserID, settings)
	if err != nil {
		return nil, err
	}
	err = a.client.EnsureAccess(ctx, actor.GuildID, channelID, actor.DiscordUserID, settings.StaffRoleDiscordIDs)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = a.client.DeleteChannel(ctx, channelID)
		return nil, err
	}
	ticket, err := store.finishOpening(ctx, actor, token, channelID, a.service.now())
	if err != nil {
		// The commit may have landed even though it reported an error, so
		// keep the channel rather than delete a ticket that might exist.
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
	ticket, err := a.service.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if err := a.client.SendReply(ctx, ticket.ThreadDiscordChannelID, body); err != nil {
		return err
	}
	return a.service.Reply(ctx, actor, ticketID, body)
}

// Close saves the transcript, resolves the ticket, and archives its
// channel. It needs a moderator. Retrying after an archive failure only
// retries the archive.
func (a *DiscordAdapter) Close(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	if !actor.CanModerate {
		return nil, ErrPermissionDenied
	}
	return a.archive(ctx, actor, ticketID, StatusResolved, func(transcript string) (*Ticket, error) {
		return a.service.Resolve(ctx, actor, ticketID, transcript)
	})
}

// Cancel is Close for a withdrawal by the owner or a moderator. Nothing
// calls it yet: the HTTP cancel route uses Service.Cancel, which leaves the
// ticket's channel open and saves no transcript.
func (a *DiscordAdapter) Cancel(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	return a.archive(ctx, actor, ticketID, StatusCancelled, func(transcript string) (*Ticket, error) {
		return a.service.cancel(ctx, actor, ticketID, &transcript)
	})
}

// RepairPermissions resets the ticket's ACL to owner, staff, and bot, and
// records the repair. It needs Manage Guild.
func (a *DiscordAdapter) RepairPermissions(ctx context.Context, actor modules.Actor, ticketID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	ticket, err := a.service.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	settings, err := a.service.enabledSettings(ctx, actor.GuildID)
	if err != nil {
		return err
	}
	err = a.client.EnsureAccess(ctx, actor.GuildID, ticket.ThreadDiscordChannelID,
		ticket.OwnerDiscordUserID, settings.StaffRoleDiscordIDs)
	if err != nil {
		return err
	}
	return a.service.RecordPermissionsRepaired(ctx, actor.GuildID, ticketID)
}

// archive closes a ticket in two steps: an open ticket's transcript is
// captured and finish moves it to closed, then its channel is archived. A
// ticket already in closed skips to the archive, so a retry finishes a
// close whose archive failed.
func (a *DiscordAdapter) archive(ctx context.Context, actor modules.Actor, ticketID string, closed Status, finish func(transcript string) (*Ticket, error)) (*Ticket, error) {
	ticket, err := a.service.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	result := ticket
	switch ticket.Status {
	case StatusOpen:
		transcript, err := a.client.CaptureTranscript(ctx, ticket.ThreadDiscordChannelID)
		if err != nil {
			return nil, err
		}
		if result, err = finish(transcript); err != nil {
			return nil, err
		}
	case closed:
	default:
		return nil, ErrInvalidTransition
	}
	if err := a.client.ArchiveChannel(ctx, ticket.ThreadDiscordChannelID); err != nil {
		return result, err
	}
	return result, nil
}
