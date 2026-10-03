package tickets

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// componentNamespace prefixes every ticket button and modal custom ID.
const componentNamespace = "ticket"

// EntryComponents are the controls meant for the entry channel: open a
// ticket, and the staff queue. Nothing posts them yet, so until something
// does, members have no button to open a ticket with.
func EntryComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(
		discord.Button(customID("open", ""), "Open ticket", discordgo.PrimaryButton, false),
		discord.Button(customID("queue", ""), "Staff queue", discordgo.SecondaryButton, false),
	)}
}

// TicketComponents are one ticket's view, reply, and close controls.
func TicketComponents(ticketID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(
		discord.Button(customID("view", ticketID), "View", discordgo.SecondaryButton, false),
		discord.Button(customID("reply", ticketID), "Reply", discordgo.PrimaryButton, false),
		discord.Button(customID("close", ticketID), "Close", discordgo.DangerButton, false),
	)}
}

// customID encodes a ticket button or modal custom ID.
func customID(action, ticketID string) string {
	return discord.MustCustomID(discord.CustomID{
		Namespace: componentNamespace,
		Action:    action,
		Version:   "v1",
		Payload:   ticketID,
	})
}

// ticketControls is TicketComponents plus, for managers, a repair button.
func ticketControls(ticketID string, includeRepair bool) []discordgo.MessageComponent {
	components := TicketComponents(ticketID)
	if !includeRepair {
		return components
	}
	row := components[0].(discordgo.ActionsRow)
	repair := discord.Button(customID("repair", ticketID), "Repair permissions", discordgo.SecondaryButton, false)
	row.Components = append(row.Components, repair)
	components[0] = row
	return components
}

// openComponent acknowledges, then provisions the member's private ticket.
func (m *Module) openComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		ticket, err := m.discord.Open(ctx, actor)
		if err != nil {
			return showError(responder, err)
		}
		message := discord.Content("Ticket opened: <#"+ticket.ThreadDiscordChannelID+">", true)
		message.Components = ticketControls(ticket.ID, actor.CanManage)
		_, err = responder.EditOriginal(discord.EditMessage(message))
		return err
	})
}

// queueComponent lists open tickets for staff, without their content.
func (m *Module) queueComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		queue, err := m.service.Queue(ctx, actor, StatusOpen, 25)
		if err != nil {
			return showError(responder, err)
		}
		lines := []string{"Open tickets:"}
		for _, ticket := range queue {
			lines = append(lines, fmt.Sprintf("• `%s` — <#%s>", ticket.ID, ticket.ThreadDiscordChannelID))
		}
		if len(queue) == 0 {
			lines = append(lines, "No open tickets.")
		}
		_, err = responder.EditOriginal(discord.EditMessage(discord.Content(strings.Join(lines, "\n"), true)))
		return err
	})
}

// viewComponent shows a ticket's state and timeline to its owner or staff.
func (m *Module) viewComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentTicketID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		ticket, events, err := m.service.Detail(ctx, actor, ticketID)
		if err != nil {
			return showError(responder, err)
		}
		lines := []string{fmt.Sprintf("Ticket `%s` is **%s**.", ticket.ID, ticket.Status)}
		for _, event := range events {
			lines = append(lines, fmt.Sprintf("• %s — %s", event.Type, discord.Truncate(event.Body, 240)))
		}
		message := discord.Content(strings.Join(lines, "\n"), true)
		message.Components = ticketControls(ticket.ID, actor.CanManage)
		_, err = responder.EditOriginal(discord.EditMessage(message))
		return err
	})
}

// repairComponent restores a ticket's private ACL, for managers.
func (m *Module) repairComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentTicketID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		if err := m.discord.RepairPermissions(ctx, actor, ticketID); err != nil {
			return showError(responder, err)
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Ticket permissions repaired.", true)))
		return err
	})
}

// replyComponent opens the reply modal, so reply text never goes in a
// custom ID.
func (m *Module) replyComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentTicketID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	components := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.TextInput{
			CustomID:  "body",
			Label:     "Reply",
			Style:     discordgo.TextInputParagraph,
			Required:  true,
			MinLength: 1,
			MaxLength: 4000,
		},
	}}}
	return discord.Immediate(discord.Modal("Reply to ticket", customID("reply-submit", ticketID), components))
}

// submitReplyModal posts the reply in the ticket.
func (m *Module) submitReplyModal(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	data := interaction.ModalSubmitData()
	id, err := discord.DecodeCustomID(data.CustomID)
	if err != nil || id.Payload == "" {
		return discord.Immediate(discord.Error("That ticket reply is invalid."))
	}
	body := strings.TrimSpace(discord.ModalValue(data, "body"))
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		if err := m.discord.Reply(ctx, actor, id.Payload, body); err != nil {
			return showError(responder, err)
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Reply sent.", true)))
		return err
	})
}

// closeComponent saves the transcript and resolves the ticket.
func (m *Module) closeComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentTicketID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		if _, err := m.discord.Close(ctx, actor, ticketID); err != nil {
			return showError(responder, err)
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Ticket closed and transcript captured.", true)))
		return err
	})
}

// componentTicketID returns the ticket ID a button carries. It proves
// nothing: the task checks live authority and the service checks access.
func componentTicketID(interaction *discordgo.InteractionCreate) (string, error) {
	if interaction.GuildID == "" {
		return "", errors.New("ticket interactions require a guild")
	}
	id, err := discord.DecodeCustomID(interaction.MessageComponentData().CustomID)
	if err != nil || id.Payload == "" {
		return "", errors.New("ticket id is required")
	}
	return id.Payload, nil
}

// task defers the response, resolves the caller's live authority, and runs
// fn. Discord's interaction permissions and the gateway cache never grant
// staff authority.
func (m *Module) task(interaction *discordgo.InteractionCreate, fn func(context.Context, discord.Responder, modules.Actor) error) discord.Result {
	return discord.Async(discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder) error {
		ctx = quack.ContextWithAuditSource(ctx, quack.AuditSourceDiscord)
		actor, err := m.actor(ctx, interaction)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit("Quack could not verify your ticket access."))
			return nil
		}
		return fn(ctx, responder, actor)
	})
}

// actor resolves the interaction's member against live Discord state.
func (m *Module) actor(ctx context.Context, interaction *discordgo.InteractionCreate) (modules.Actor, error) {
	if interaction.GuildID == "" {
		return modules.Actor{}, errors.New("ticket interactions require a guild")
	}
	userID := ""
	if interaction.Member != nil && interaction.Member.User != nil {
		userID = interaction.Member.User.ID
	} else if interaction.User != nil {
		userID = interaction.User.ID
	}
	if userID == "" {
		return modules.Actor{}, errors.New("ticket interaction user is unavailable")
	}
	staff, err := m.staff.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID, DiscordUserID: userID,
	})
	if err != nil || staff == nil || !staff.Live.Actor.Present {
		return modules.Actor{}, quack.ErrAuthorizationDenied
	}
	return modules.ActorFor(staff), nil
}

// showError replaces the deferred response with a message safe to show the
// member. The interaction itself succeeded, so it returns nil.
func showError(responder discord.Responder, err error) error {
	_, _ = responder.EditOriginal(discord.ErrorEdit(errorMessage(err)))
	return nil
}

// errorMessage maps an error to text safe to show the member.
func errorMessage(err error) string {
	switch {
	case errors.Is(err, ErrDisabled):
		return "Tickets are not enabled for this server."
	case errors.Is(err, ErrPermissionDenied):
		return "You do not have permission to use that ticket."
	case errors.Is(err, ErrDuplicateOpen):
		return "You already have an open ticket."
	case errors.Is(err, ErrRateLimited):
		return "You have reached this server's ticket limit."
	case errors.Is(err, ErrNotFound):
		return "That ticket was not found."
	default:
		return "Quack could not complete that ticket operation."
	}
}
