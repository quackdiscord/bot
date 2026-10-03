package moduleintegration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// ticketActor resolves the current Discord member into module authority.
func (r *Runtime) ticketActor(ctx context.Context, interaction *discordgo.InteractionCreate) (tickets.Actor, error) {
	if interaction.GuildID == "" {
		return tickets.Actor{}, errors.New("ticket interactions require a guild")
	}
	userID := interactionUserID(interaction)
	if userID == "" {
		return tickets.Actor{}, errors.New("ticket interaction user is unavailable")
	}
	if r == nil || r.services == nil || r.services.Guilds == nil {
		return tickets.Actor{}, errors.New("live ticket authorization is unavailable")
	}
	guildContext, err := r.services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: interaction.GuildID, DiscordUserID: userID,
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
func (r *Runtime) openTicketComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		ticket, err := r.TicketDiscord.Open(taskCtx, actor)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		message := discord.Content("Ticket opened: <#"+ticket.ThreadDiscordChannelID+">", true)
		message.Components = ticketControls(ticket.ID, actor.CanManage)
		_, err = responder.EditOriginal(discord.EditMessage(message))
		return err
	})
}

// ticketQueueComponent returns a bounded staff queue without transcript content.
func (r *Runtime) ticketQueueComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		queue, err := r.Tickets.Queue(taskCtx, actor, tickets.StatusOpen, 25)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
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

// viewTicketComponent returns private ticket state and its immutable timeline.
func (r *Runtime) viewTicketComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := ticketComponentID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		ticket, events, err := r.Tickets.Detail(taskCtx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
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

// repairTicketComponent restores the configured private ACL for current managers.
func (r *Runtime) repairTicketComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := ticketComponentID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		if err := r.TicketDiscord.RepairPermissions(taskCtx, actor, ticketID); err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Ticket permissions repaired.", true)))
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
	repairID := discord.MustCustomID(discord.CustomID{Namespace: "ticket", Action: "repair", Version: "v1", Payload: ticketID})
	row.Components = append(row.Components, discord.Button(repairID, "Repair permissions", discordgo.SecondaryButton, false))
	components[0] = row
	return components
}

// replyTicketComponent opens a modal so reply content never enters a custom ID.
func (r *Runtime) replyTicketComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := ticketComponentID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	customID := discord.MustCustomID(discord.CustomID{Namespace: "ticket", Action: "reply-submit", Version: "v1", Payload: ticketID})
	components := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.TextInput{CustomID: "body", Label: "Reply", Style: discordgo.TextInputParagraph, Required: true, MinLength: 1, MaxLength: 4000},
	}}}
	return discord.Immediate(discord.Modal("Reply to ticket", customID, components))
}

// submitTicketReplyModal validates the modal and sends through the private adapter.
func (r *Runtime) submitTicketReplyModal(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	data := interaction.ModalSubmitData()
	customID, err := discord.DecodeCustomID(data.CustomID)
	if err != nil || customID.Payload == "" {
		return discord.Immediate(discord.Error("That ticket reply is invalid."))
	}
	body := strings.TrimSpace(discord.ModalValue(data, "body"))
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		if err := r.TicketDiscord.Reply(taskCtx, actor, customID.Payload, body); err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Reply sent.", true)))
		return err
	})
}

// closeTicketComponent captures the transcript and resolves the ticket.
func (r *Runtime) closeTicketComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := ticketComponentID(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return r.ticketTask(interaction, func(taskCtx context.Context, responder discord.Responder, actor tickets.Actor) error {
		if _, err := r.TicketDiscord.Close(taskCtx, actor, ticketID); err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(ticketErrorMessage(err)))
			return nil
		}
		_, err := responder.EditOriginal(discord.EditMessage(discord.Content("Ticket closed and transcript captured.", true)))
		return err
	})
}

// ticketComponentID extracts only the opaque identity; ticketTask checks live
// membership and the service verifies ownership before any content is read.
func ticketComponentID(interaction *discordgo.InteractionCreate) (string, error) {
	if interaction.GuildID == "" {
		return "", errors.New("ticket interactions require a guild")
	}
	customID, err := discord.DecodeCustomID(interaction.MessageComponentData().CustomID)
	if err != nil || customID.Payload == "" {
		return "", errors.New("ticket id is required")
	}
	return customID.Payload, nil
}

// ticketTask acknowledges before making fresh Discord authorization requests.
// Gateway cache and channel-level overrides never grant guild staff authority.
func (r *Runtime) ticketTask(interaction *discordgo.InteractionCreate, task func(context.Context, discord.Responder, tickets.Actor) error) discord.Result {
	return discord.Async(discord.DeferEphemeral(), func(taskCtx context.Context, responder discord.Responder) error {
		taskCtx = quack.ContextWithAuditSource(taskCtx, quack.AuditSourceDiscord)
		actor, err := r.ticketActor(taskCtx, interaction)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit("Quack could not verify your ticket access."))
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
