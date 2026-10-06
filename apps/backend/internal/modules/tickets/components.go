package tickets

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// openComponent acknowledges, then opens the member's ticket, or points
// them at the one they already hold.
func (m *Module) openComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		ticket, err := m.adapter.Open(ctx, actor)
		if errors.Is(err, ErrDuplicateOpen) {
			if active, lookupErr := m.service.ActiveForMember(ctx, actor); lookupErr == nil {
				_, err = responder.EditOriginal(discord.EditMessage(existingTicketMessage(active)))
				return err
			}
		}
		if ticket == nil {
			return showError(ctx, responder, err)
		}
		if err != nil {
			slog.WarnContext(ctx, "Ticket opened without finishing setup", "ticket_id", ticket.ID, "error", err)
		}
		_, err = responder.EditOriginal(discord.EditMessage(openedMessage(ticket, err)))
		return err
	})
}

// queueComponent lists open tickets for staff, without their content. No
// current message carries it; it stays routed for older ones.
func (m *Module) queueComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		queue, err := m.service.Queue(ctx, actor, StatusOpen, 25)
		if err != nil {
			return showError(ctx, responder, err)
		}
		_, err = responder.EditOriginal(discord.EditMessage(queueListMessage(queue)))
		return err
	})
}

// viewComponent shows a ticket's detail and recovery controls. A private
// view is refreshed in place, so paging and stale thread links update; a
// press on a public post gets its own private reply.
func (m *Module) viewComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	payload, err := componentPayload(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	ticketID, page := detailPayload(payload)
	acknowledgement := discord.DeferEphemeral()
	if message := interaction.Message; message != nil && message.Flags&discordgo.MessageFlagsEphemeral != 0 {
		acknowledgement = discord.DeferUpdate()
	}
	return m.task(interaction, acknowledgement, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		ticket, _, err := m.service.Detail(ctx, actor, ticketID)
		if err != nil {
			return showError(ctx, responder, err)
		}
		pending, err := m.service.ClosurePending(ctx, actor, ticketID)
		if err != nil {
			return showError(ctx, responder, err)
		}
		var transcript *Transcript
		if ticket.Status != StatusOpen {
			transcript, err = m.service.Transcript(ctx, actor, ticketID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return showError(ctx, responder, err)
			}
		}
		_, err = responder.EditOriginal(discord.EditMessage(detailMessage(ticket, nil, actor, pending, transcript, page)))
		return err
	})
}

// repairComponent restores an open ticket's access, for managers.
func (m *Module) repairComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentPayload(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		if err := m.adapter.RepairPermissions(ctx, actor, ticketID); err != nil {
			return showError(ctx, responder, err)
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Signal("lock",
			"Ticket access is repaired. Access is limited to the member and staff.", true)))
		return err
	})
}

// closeComponent closes a ticket, or resumes a close that stopped.
func (m *Module) closeComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentPayload(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		return closeWithFeedback(ctx, responder, m.adapter, actor, ticketID, interaction.ChannelID)
	})
}

// progressCloser closes a ticket, reporting before the thread is deleted.
// *DiscordAdapter implements it.
type progressCloser interface {
	CloseWithProgress(ctx context.Context, actor modules.Actor, ticketID string, beforeDelete func(*Ticket) error) (*Ticket, error)
}

// closeWithFeedback closes ticketID. When the button was pressed inside the
// ticket's own thread, the answer is given before the thread is deleted,
// since the interaction dies with it; elsewhere the final result is shown.
func closeWithFeedback(ctx context.Context, responder discord.Responder, closer progressCloser, actor modules.Actor, ticketID, originChannelID string) error {
	ticket, err := closer.CloseWithProgress(ctx, actor, ticketID, func(ticket *Ticket) error {
		if ticket.ThreadDiscordChannelID != originChannelID {
			return nil
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Signal("lock", closedCopy(ticket), true)))
		return err
	})
	if err != nil {
		if !expectedError(err) {
			slog.ErrorContext(ctx, "Ticket close failed", "ticket_id", ticketID, "error", err)
		}
		message := closeFailureMessage(ticket, err)
		if ticket == nil && refusedFor2FA(ctx, err) {
			message = discord.Signal("error", discord.MFARequiredMessage, true)
		}
		_, editErr := responder.EditOriginal(discord.EditMessage(message))
		return editErr
	}
	if ticket.ThreadDiscordChannelID == originChannelID {
		return nil
	}
	text := strings.Replace(closedCopy(ticket), "This ticket is closing.", "Ticket closed.", 1)
	_, err = responder.EditOriginal(discord.EditMessage(discord.Signal("lock", text, true)))
	return err
}

// componentPayload returns the payload a ticket button carries. It proves
// nothing: the task checks live authority and the service checks access.
func componentPayload(interaction *discordgo.InteractionCreate) (string, error) {
	if interaction.GuildID == "" {
		return "", errors.New("ticket interactions require a guild")
	}
	id, err := discord.DecodeCustomID(interaction.MessageComponentData().CustomID)
	if err != nil || id.Payload == "" {
		return "", errors.New("ticket id is required")
	}
	return id.Payload, nil
}

// task acknowledges with acknowledgement, resolves the caller's live
// authority, and runs fn. Discord's interaction permissions and the gateway
// cache never grant staff authority.
//
// A member whom only the guild's 2FA requirement keeps from being staff
// acts as a member, so their own ticket still works. If fn is refused for
// lack of staff authority, the reply tells them how to fix it. Refusals
// are not audited.
func (m *Module) task(interaction *discordgo.InteractionCreate, acknowledgement *discordgo.InteractionResponse, fn func(context.Context, discord.Responder, modules.Actor) error) discord.Result {
	return discord.Async(acknowledgement, func(ctx context.Context, responder discord.Responder) error {
		ctx = quack.ContextWithAuditSource(ctx, quack.AuditSourceDiscord)
		actor, staff, err := m.actor(ctx, interaction)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit("Quack could not verify your ticket access."))
			return nil
		}
		if staff.MFARequired {
			ctx = context.WithValue(ctx, mfaBlockedKey{}, true)
		}
		return fn(ctx, responder, actor)
	})
}

// mfaBlockedKey marks a context whose member only the guild's 2FA
// requirement keeps from being staff. See task.
type mfaBlockedKey struct{}

// refusedFor2FA reports whether err refused staff authority that only the
// guild's 2FA requirement withholds.
func refusedFor2FA(ctx context.Context, err error) bool {
	blocked, _ := ctx.Value(mfaBlockedKey{}).(bool)
	return blocked && errors.Is(err, ErrPermissionDenied)
}

// actor resolves the interaction's member against live Discord state, and
// returns the staff context it came from.
func (m *Module) actor(ctx context.Context, interaction *discordgo.InteractionCreate) (modules.Actor, *quack.GuildStaffContext, error) {
	if interaction.GuildID == "" {
		return modules.Actor{}, nil, errors.New("ticket interactions require a guild")
	}
	userID := ""
	if interaction.Member != nil && interaction.Member.User != nil {
		userID = interaction.Member.User.ID
	} else if interaction.User != nil {
		userID = interaction.User.ID
	}
	if userID == "" {
		return modules.Actor{}, nil, errors.New("ticket interaction user is unavailable")
	}
	staff, err := m.staff.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID, DiscordUserID: userID,
	})
	if err != nil || staff == nil || !staff.Live.Actor.Present {
		return modules.Actor{}, nil, quack.ErrAuthorizationDenied
	}
	return modules.ActorFor(staff), staff, nil
}

// showError replaces the deferred response with copy safe to show the
// member, and logs the error, since returning nil keeps the router from
// doing so. Refusals the member caused are only debug noise.
func showError(ctx context.Context, responder discord.Responder, err error) error {
	if expectedError(err) {
		slog.DebugContext(ctx, "Ticket interaction refused", "error", err)
	} else {
		slog.ErrorContext(ctx, "Ticket interaction failed", "error", err)
	}
	message := errorMessage(err)
	if refusedFor2FA(ctx, err) {
		message = discord.MFARequiredMessage
	}
	_, _ = responder.EditOriginal(discord.ErrorEdit(message))
	return nil
}
