package tickets

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
)

// DiscordClient is what DiscordAdapter needs from Discord. guildID is
// always Quack's internal guild ID. The module's channels type implements
// it; tests use a fake.
type DiscordClient interface {
	// CreateThread starts a ticket's private thread under the entry channel
	// and returns its ID.
	CreateThread(ctx context.Context, guildID, ownerID string, settings Settings) (string, error)
	// EnsureAccess invites the owner and removes members who are no longer
	// staff.
	EnsureAccess(ctx context.Context, guildID, threadID, ownerID string) error
	// SendWelcome greets the owner in a new thread with a Close button.
	SendWelcome(ctx context.Context, ticket *Ticket) error
	// FreezeThread archives and locks a thread so the history stops
	// changing before it is captured.
	FreezeThread(ctx context.Context, threadID string) error
	// CaptureMessages reads a thread's whole surviving history.
	CaptureMessages(ctx context.Context, threadID string) ([]TranscriptMessage, error)
	// DeleteThread deletes a closed ticket's thread. A thread already gone
	// counts as deleted.
	DeleteThread(ctx context.Context, threadID string) error
	// PublishQueue posts or edits the ticket's staff queue post; with a
	// transcript it marks the ticket closed and attaches the transcript. A
	// definite refusal wraps ErrQueueNotSent, and a saved post that is gone
	// returns ErrQueueMessageMissing.
	PublishQueue(ctx context.Context, ticket *Ticket, settings Settings, transcript *Transcript) (*QueueReceipt, error)
	// QueueMessageExists reports whether a saved queue post still exists.
	// Only Discord saying it is gone yields false without an error.
	QueueMessageExists(ctx context.Context, channelID, messageID string) (bool, error)
	// ValidateQueueMessage checks that a message link an administrator gave
	// is Quack's queue post for ticket.
	ValidateQueueMessage(ctx context.Context, ticket *Ticket, messageURL string) (*QueueReceipt, error)
	// DeliverCloseNotice DMs the member the transcript and returns the DM's
	// ID. With reconcileOnly it only looks for an earlier DM whose send was
	// uncertain. A definite refusal wraps ErrCloseNoticeNotSent.
	DeliverCloseNotice(ctx context.Context, ticket *Ticket, transcript *Transcript, reconcileOnly bool) (string, error)
}

// DiscordAdapter runs the ticket operations that also change Discord. The
// Service holds the rules; the adapter orders the Discord calls around them
// so a failure part way leaves nothing a retry cannot finish, and nothing
// is deleted before its transcript is safe.
type DiscordAdapter struct {
	service *Service
	client  DiscordClient
	// closes serializes each ticket's close, repair, and recovery in this
	// process.
	closes keyedLocks
}

// NewDiscordAdapter returns a DiscordAdapter over service and client.
func NewDiscordAdapter(service *Service, client DiscordClient) *DiscordAdapter {
	return &DiscordAdapter{service: service, client: client}
}

// Open reserves the member's ticket slot, starts a private thread, and
// commits the ticket before inviting anyone, so the journal knows the
// thread before its first message. A ticket returned with an error was
// saved but its setup did not finish; it keeps the member's slot, and an
// administrator can repair it.
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
	if settings.QueueChannelDiscordID == "" {
		return nil, errors.New("ticket queue channel is not configured")
	}
	store := a.service.store
	token, err := store.reserveOpening(ctx, actor, a.service.now())
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
	threadID, err := a.client.CreateThread(ctx, actor.GuildID, actor.DiscordUserID, settings)
	if err != nil {
		return nil, err
	}
	ticket, err := store.finishOpening(ctx, actor, token, threadID, a.service.now())
	if err != nil {
		// The commit may have landed even though it reported an error, so
		// keep the thread rather than delete a ticket that might exist.
		a.service.audit(ctx, actor, "ticket.open", token, "failure", err)
		return nil, err
	}
	a.service.rememberJournalThread(ticket)
	a.service.audit(ctx, actor, "ticket.open", ticket.ID, "success", nil)
	if err := a.client.EnsureAccess(ctx, actor.GuildID, threadID, actor.DiscordUserID); err != nil {
		// The owner may already be in and posting. Keep the ticket and
		// still tell staff, so it can be repaired.
		_, queueErr := a.publishQueue(ctx, ticket, settings, nil)
		return ticket, errors.Join(err, queueErr)
	}
	// A failed greeting must not keep staff from hearing about the ticket.
	welcomeErr := a.client.SendWelcome(ctx, ticket)
	_, queueErr := a.publishQueue(ctx, ticket, settings, nil)
	return ticket, errors.Join(welcomeErr, queueErr)
}

// Close closes a ticket; see CloseWithProgress.
func (a *DiscordAdapter) Close(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error) {
	return a.CloseWithProgress(ctx, actor, ticketID, nil)
}

// CloseWithProgress closes a ticket for its owner or a moderator, even with
// tickets switched off. It locks the thread, saves the transcript, attaches
// it to the queue post, DMs the member, and deletes the thread; the member
// can open another ticket only after that. beforeDelete, if set, runs once
// the transcript is safe and before the thread goes, so a caller inside the
// thread can still answer. Each step is safe to retry: a later call resumes
// where an earlier one stopped, and the returned ticket says how far it got.
func (a *DiscordAdapter) CloseWithProgress(ctx context.Context, actor modules.Actor, ticketID string, beforeDelete func(*Ticket) error) (*Ticket, error) {
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+ticketID)
	if err != nil {
		return nil, err
	}
	defer release()
	ticket, err := a.service.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return nil, err
	}
	resolved := ticket
	switch ticket.Status {
	case StatusOpen:
		if err := a.client.FreezeThread(ctx, ticket.ThreadDiscordChannelID); err != nil {
			return ticket, err
		}
		messages, err := a.client.CaptureMessages(ctx, ticket.ThreadDiscordChannelID)
		if err != nil {
			return ticket, err
		}
		if resolved, err = a.service.ResolveWithHistory(ctx, actor, ticketID, messages); err != nil {
			return ticket, err
		}
	case StatusResolved:
	default:
		return nil, ErrInvalidTransition
	}
	// A transcript already published is trusted only while its post still
	// exists; a post Discord says is gone is published again.
	if resolved.TranscriptURL != "" {
		if err := a.checkQueueReceipt(ctx, resolved); err != nil {
			return resolved, err
		}
	}
	if resolved.TranscriptURL == "" {
		transcript, err := a.service.Transcript(ctx, actor, ticketID)
		if err != nil {
			return resolved, err
		}
		settings, _, err := a.service.loadSettings(ctx, actor.GuildID)
		if err != nil {
			return resolved, err
		}
		if _, err := a.publishQueue(ctx, resolved, settings, transcript); err != nil {
			return resolved, err
		}
	}
	if err := a.deliverCloseNotice(ctx, actor, resolved); err != nil {
		return resolved, err
	}
	if beforeDelete != nil {
		if err := beforeDelete(resolved); err != nil {
			return resolved, err
		}
	}
	if err := a.client.DeleteThread(ctx, ticket.ThreadDiscordChannelID); err != nil {
		return resolved, err
	}
	return resolved, a.service.store.finishClosure(ctx, actor.GuildID, ticket.ID, a.service.now())
}

// RepairPermissions re-invites an open ticket's owner, removes former
// staff, and posts the queue post again if Discord says it is gone. A post
// whose delivery is uncertain is left for QueueRecovery. It needs Manage
// Guild.
func (a *DiscordAdapter) RepairPermissions(ctx context.Context, actor modules.Actor, ticketID string) error {
	if !actor.CanManage {
		return ErrPermissionDenied
	}
	release, err := a.closes.acquire(ctx, actor.GuildID+":"+ticketID)
	if err != nil {
		return err
	}
	defer release()
	ticket, err := a.service.visibleTicket(ctx, actor, ticketID)
	if err != nil {
		return err
	}
	if ticket.Status != StatusOpen {
		return ErrInvalidTransition
	}
	settings, enabled, err := a.service.loadSettings(ctx, actor.GuildID)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrDisabled
	}
	err = a.client.EnsureAccess(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, ticket.OwnerDiscordUserID)
	if err != nil {
		return err
	}
	if err := a.checkQueueReceipt(ctx, ticket); err != nil {
		return err
	}
	if ticket.LogMessageDiscordID == "" {
		if _, err := a.publishQueue(ctx, ticket, settings, nil); err != nil {
			return err
		}
	}
	return a.service.RecordPermissionsRepaired(ctx, actor.GuildID, ticketID)
}
