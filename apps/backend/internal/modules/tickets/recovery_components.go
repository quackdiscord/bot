package tickets

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// staleRecovery answers recovery controls whose payload no longer makes
// sense.
const staleRecovery = "Open the ticket's recovery controls again."

// queueFixComponent shows a manager the uncertain queue send and the two
// decisions they can make about it, bound to that one attempt.
func (m *Module) queueFixComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	ticketID, err := componentPayload(interaction)
	if err != nil {
		return discord.Immediate(discord.Error("That ticket is unavailable."))
	}
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		recovery, err := m.adapter.QueueRecovery(ctx, actor, ticketID)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(recoveryErrorMessage(err)))
			return nil
		}
		message := discord.Signal("ticket", "Check the staff queue in <#"+recovery.ChannelDiscordID+"> for this ticket's post. If it exists, use its message link. If no post was sent, you can allow one replacement after confirming that below.", true)
		payload := recovery.TicketID + "~" + recovery.AttemptID
		message.Components = []discordgo.MessageComponent{discord.Row(
			button("queueadopt", payload, "Use existing post", discordgo.SecondaryButton),
			button("queueretry", payload, "Post was not sent", discordgo.SecondaryButton),
		)}
		_, err = responder.EditOriginal(discord.EditMessage(message))
		return err
	})
}

// queueAdoptComponent asks for the link of the post that did arrive. The
// form shows nothing private; the submission is authorized again.
func (m *Module) queueAdoptComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	payload, err := componentPayload(interaction)
	if _, _, ok := recoveryPayload(payload); err != nil || !ok {
		return discord.Immediate(discord.Error(staleRecovery))
	}
	return discord.Immediate(discord.Modal("Use existing queue post", customID("queueadopt", payload), []discordgo.MessageComponent{
		discord.Row(discordgo.TextInput{
			CustomID:  "message",
			Label:     "Queue message link",
			Style:     discordgo.TextInputShort,
			Required:  true,
			MaxLength: 300,
		}),
	}))
}

// queueAdoptSubmit adopts the linked post once it is verified as this
// ticket's queue post.
func (m *Module) queueAdoptSubmit(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	data := interaction.ModalSubmitData()
	id, err := discord.DecodeCustomID(data.CustomID)
	ticketID, attemptID, ok := recoveryPayload(id.Payload)
	if err != nil || !ok {
		return discord.Immediate(discord.Error(staleRecovery))
	}
	return m.reconcile(interaction, QueueRecoveryInput{
		TicketID:   ticketID,
		AttemptID:  attemptID,
		MessageURL: strings.TrimSpace(discord.ModalValue(data, "message")),
	})
}

// queueRetryComponent asks the manager to confirm no post arrived, since a
// replacement could otherwise duplicate it.
func (m *Module) queueRetryComponent(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	payload, err := componentPayload(interaction)
	if _, _, ok := recoveryPayload(payload); err != nil || !ok {
		return discord.Immediate(discord.Error(staleRecovery))
	}
	message := discord.Signal("ticket", "Only continue after checking the staff queue and confirming that this ticket's post was not sent. If it did arrive, allowing a replacement may create a duplicate. Use its existing message link instead.", true)
	message.Components = []discordgo.MessageComponent{discord.Row(
		button("queueretryok", payload, "Confirm no post was sent", discordgo.DangerButton),
	)}
	return discord.Immediate(discord.Ephemeral(message))
}

// queueRetryConfirmed records that the inspected attempt was not delivered.
func (m *Module) queueRetryConfirmed(_ context.Context, interaction *discordgo.InteractionCreate) discord.Result {
	payload, err := componentPayload(interaction)
	ticketID, attemptID, ok := recoveryPayload(payload)
	if err != nil || !ok {
		return discord.Immediate(discord.Error(staleRecovery))
	}
	return m.reconcile(interaction, QueueRecoveryInput{TicketID: ticketID, AttemptID: attemptID, ConfirmNotDelivered: true})
}

// reconcile records a recovery decision with fresh authority. Sending and
// closing are left to the ordinary repair and close controls it offers.
func (m *Module) reconcile(interaction *discordgo.InteractionCreate, input QueueRecoveryInput) discord.Result {
	return m.task(interaction, discord.DeferEphemeral(), func(ctx context.Context, responder discord.Responder, actor modules.Actor) error {
		ticket, err := m.adapter.ReconcileQueue(ctx, actor, input)
		if err != nil {
			_, _ = responder.EditOriginal(discord.ErrorEdit(recoveryErrorMessage(err)))
			return nil
		}
		text := "The existing queue post is linked to this ticket."
		if input.ConfirmNotDelivered {
			text = "Your confirmation was saved. Quack can now send one replacement."
		}
		var components []discordgo.MessageComponent
		switch {
		case ticket.Status == StatusOpen:
			if input.ConfirmNotDelivered {
				text += " Use Repair ticket to finish setup."
			}
			components = ticketControls(ticket.ID, true)
		default:
			text += " Finish closing to save the transcript before deleting the thread."
			components = finishClosingComponents(ticket.ID)
		}
		message := discord.Signal("ticket", text, true)
		message.Components = components
		_, err = responder.EditOriginal(discord.EditMessage(message))
		return err
	})
}

// recoveryPayload splits "ticketID~attemptID", rejecting anything missing
// or ambiguous. Authority and the attempt are checked on submit.
func recoveryPayload(payload string) (string, string, bool) {
	ticketID, attemptID, ok := strings.Cut(payload, "~")
	return ticketID, attemptID, ok && ticketID != "" && attemptID != "" && !strings.Contains(attemptID, "~")
}

// recoveryErrorMessage sends stale controls back to the ticket's current
// state, without implying anything was cleared or sent.
func recoveryErrorMessage(err error) string {
	if errors.Is(err, ErrInvalidTransition) || errors.Is(err, ErrQueueDeliveryUnknown) {
		return "This ticket's recovery state has changed. Open View ticket again to check its current status. No replacement was sent."
	}
	return errorMessage(err)
}
