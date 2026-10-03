package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// ticketActor resolves the current Discord member into module authority.
func (r *Runtime) ticketActor(ctx ui.Context) (tickets.Actor, error) {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return tickets.Actor{}, errors.New("ticket interactions require a guild")
	}
	userID := interactionUserID(ctx.Interaction)
	if userID == "" {
		return tickets.Actor{}, errors.New("ticket interaction user is unavailable")
	}
	if r == nil || r.services == nil || r.services.Guilds == nil {
		return tickets.Actor{}, errors.New("live ticket authorization is unavailable")
	}
	guildContext, err := r.services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: ctx.Interaction.GuildID, DiscordUserID: userID,
	})
	if err != nil || guildContext == nil || !guildContext.Live.Actor.Present {
		return tickets.Actor{}, quack.ErrAuthorizationDenied
	}
	return tickets.Actor{
		GuildID: guildContext.Guild.ID, DiscordUserID: userID,
		CanManage:   guildContext.Can(quack.PermissionActionGuildSettingsWrite),
		CanModerate: guildContext.Can(quack.PermissionActionTicketResolve),
	}, nil
}

// openTicketComponent acknowledges quickly, then provisions the private ticket.
func (r *Runtime) openTicketComponent(ctx ui.Context) ui.HandlerResult {
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, err := r.TicketDiscord.Open(taskCtx, actor)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		message := ui.Content("Ticket opened: <#"+ticket.ThreadDiscordChannelID+">", true)
		message.Components = ticketControls(ticket.ID, actor.CanManage)
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}

// ticketQueueComponent returns a bounded staff queue without transcript content.
func (r *Runtime) ticketQueueComponent(ctx ui.Context) ui.HandlerResult {
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		queue, err := r.Tickets.Queue(taskCtx, actor, tickets.StatusOpen, 25)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		lines := []string{"Open tickets:"}
		for _, ticket := range queue {
			lines = append(lines, fmt.Sprintf("• `%s` — <#%s>", ticket.ID, ticket.ThreadDiscordChannelID))
		}
		if len(queue) == 0 {
			lines = append(lines, "No open tickets.")
		}
		_, err = responder.EditOriginal(ui.EditMessage(ui.Content(strings.Join(lines, "\n"), true)))
		return err
	})
}

// viewTicketComponent returns private ticket state and its immutable timeline.
func (r *Runtime) viewTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		ticket, events, err := r.Tickets.Detail(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		lines := []string{fmt.Sprintf("Ticket `%s` is **%s**.", ticket.ID, ticket.Status)}
		for _, event := range events {
			lines = append(lines, fmt.Sprintf("• %s — %s", event.Type, ui.TruncateRunes(event.Body, 240)))
		}
		message := ui.Content(strings.Join(lines, "\n"), true)
		message.Components = ticketControls(ticket.ID, actor.CanManage)
		_, err = responder.EditOriginal(ui.EditMessage(message))
		return err
	})
}

// repairTicketComponent restores the configured private ACL for current managers.
func (r *Runtime) repairTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if err := r.TicketDiscord.RepairPermissions(taskCtx, actor, ticketID); err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Content("Ticket permissions repaired.", true)))
		return err
	})
}

// ticketControls adds manager-only repair behavior to the module-owned controls.
func ticketControls(ticketID string, includeRepair bool) []discordgo.MessageComponent {
	components := tickets.TicketComponents(ticketID)
	if !includeRepair || len(components) == 0 {
		return components
	}
	row, ok := components[0].(discordgo.ActionsRow)
	if !ok {
		return components
	}
	repairID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "repair", Version: "v1", Payload: ticketID})
	row.Components = append(row.Components, ui.Button(repairID, "Repair permissions", discordgo.SecondaryButton, false))
	components[0] = row
	return components
}

// replyTicketComponent opens a modal so reply content never enters a custom ID.
func (r *Runtime) replyTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	customID := ui.MustCustomID(ui.CustomID{Namespace: "ticket", Action: "reply-submit", Version: "v1", Payload: ticketID})
	components := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.TextInput{CustomID: "body", Label: "Reply", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 4000},
	}}}
	return ui.Immediate(ui.Modal("Reply to ticket", customID, components))
}

// submitTicketReplyModal validates the modal and sends through the private adapter.
func (r *Runtime) submitTicketReplyModal(ctx ui.Context) ui.HandlerResult {
	data := ctx.Interaction.ModalSubmitData()
	customID, err := ui.DecodeCustomID(data.CustomID)
	if err != nil || customID.Payload == "" {
		return ui.Immediate(ui.Error("That ticket reply is invalid."))
	}
	body := modalText(data.Components, "body")
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if err := r.TicketDiscord.Reply(taskCtx, actor, customID.Payload, body); err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Content("Reply sent.", true)))
		return err
	})
}

// closeTicketComponent captures the transcript and resolves the ticket.
func (r *Runtime) closeTicketComponent(ctx ui.Context) ui.HandlerResult {
	ticketID, err := ticketComponentID(ctx)
	if err != nil {
		return ui.Immediate(ui.Error("That ticket is unavailable."))
	}
	return r.ticketTask(ctx, func(taskCtx context.Context, responder ui.Responder, actor tickets.Actor) error {
		if _, err := r.TicketDiscord.Close(taskCtx, actor, ticketID); err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(ui.EditMessage(ui.Content("Ticket closed and transcript captured.", true)))
		return err
	})
}

// ticketComponentID extracts only the opaque identity; ticketTask checks live
// membership and the service verifies ownership before any content is read.
func ticketComponentID(ctx ui.Context) (string, error) {
	if ctx.Interaction == nil || ctx.Interaction.Interaction == nil || ctx.Interaction.GuildID == "" {
		return "", errors.New("ticket interactions require a guild")
	}
	customID, err := ui.DecodeCustomID(ctx.Interaction.MessageComponentData().CustomID)
	if err != nil || customID.Payload == "" {
		return "", errors.New("ticket id is required")
	}
	return customID.Payload, nil
}

// ticketTask acknowledges before making fresh Discord authorization requests.
// Gateway cache and channel-level overrides never grant guild staff authority.
func (r *Runtime) ticketTask(ctx ui.Context, task func(context.Context, ui.Responder, tickets.Actor) error) ui.HandlerResult {
	return ui.Async(ui.DeferEphemeral(), func(taskCtx context.Context, responder ui.Responder) error {
		taskCtx = quack.ContextWithAuditSource(taskCtx, quack.AuditSourceDiscord)
		current := ctx
		current.Context = taskCtx
		actor, err := r.ticketActor(current)
		if err != nil {
			_, _ = responder.EditOriginal(ui.ErrorEdit("Quack could not verify your ticket access."))
			return nil
		}
		return task(taskCtx, responder, actor)
	})
}

// interactionUserID normalizes guild and direct interaction identity fields.
func interactionUserID(interaction *discordgo.InteractionCreate) string {
	if interaction.Member != nil && interaction.Member.User != nil {
		return interaction.Member.User.ID
	}
	if interaction.User != nil {
		return interaction.User.ID
	}
	return ""
}

// modalText extracts one expected text input from Discord's component tree.
func modalText(components []discordgo.MessageComponent, customID string) string {
	for _, component := range components {
		row, ok := component.(*discordgo.ActionsRow)
		if !ok {
			if value, valueOK := component.(discordgo.ActionsRow); valueOK {
				row = &value
			} else {
				continue
			}
		}
		for _, child := range row.Components {
			input, ok := child.(*discordgo.TextInput)
			if ok && input.CustomID == customID {
				return strings.TrimSpace(input.Value)
			}
			if value, valueOK := child.(discordgo.TextInput); valueOK && value.CustomID == customID {
				return strings.TrimSpace(value.Value)
			}
		}
	}
	return ""
}

// ticketErrorMessage maps internal classifications to safe Discord copy.
func ticketErrorMessage(err error) string {
	switch {
	case errors.Is(err, tickets.ErrDisabled):
		return "Tickets are not enabled for this server."
	case errors.Is(err, tickets.ErrPermissionDenied):
		return "You do not have permission to use that ticket."
	case errors.Is(err, tickets.ErrDuplicateOpen):
		return "You already have an open ticket."
	case errors.Is(err, tickets.ErrRateLimited):
		return "You have reached this server's ticket limit."
	case errors.Is(err, tickets.ErrNotFound):
		return "That ticket was not found."
	default:
		return "Quack could not complete that ticket operation."
	}
}
