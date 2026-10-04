package tickets

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// componentNamespace prefixes every ticket button custom ID.
// Custom IDs live on posted messages (the entry panel, queue posts, thread
// welcomes), so existing ones must keep decoding to the same route.
const componentNamespace = "ticket"

// historyPageSize is the most ticket history one detail page shows.
const historyPageSize = 1200

// customID encodes a ticket button custom ID.
func customID(action, payload string) string {
	return discord.MustCustomID(discord.CustomID{
		Namespace: componentNamespace,
		Action:    action,
		Version:   "v1",
		Payload:   payload,
	})
}

// button is a ticket button routed to action with payload.
func button(action, payload, label string, style discordgo.ButtonStyle) discordgo.Button {
	return discord.Button(customID(action, payload), label, style, false)
}

// entryPanelMessage is the public panel in the entry channel.
func entryPanelMessage() discord.Message {
	message := discord.Content("# Need a hand?\nTalk privately with the mod team. Open a ticket and tell us what’s going on.", false)
	message.Components = []discordgo.MessageComponent{discord.Row(button("open", "", "Open ticket", discordgo.PrimaryButton))}
	return message
}

// movedPanelContent replaces a retired entry panel when the entry channel
// moves.
func movedPanelContent(entryChannelID string) string {
	return fmt.Sprintf("Tickets have moved to <#%s>. Open a ticket there.", entryChannelID)
}

// welcomeMessage greets the owner in a new thread. It may mention only the
// owner.
func welcomeMessage(ticket *Ticket) discord.Message {
	message := discord.Signal("ticket", fmt.Sprintf(
		"<@%s> A mod will be here soon. Feel free to tell us what's up while you wait.", ticket.OwnerDiscordUserID), false)
	message.Components = closeComponents(ticket.ID)
	message.AllowedMentions = &discordgo.MessageAllowedMentions{Users: []string{ticket.OwnerDiscordUserID}}
	return message
}

// queuePostMessage is a ticket's staff queue post: open, with Recovery and
// Close, or closed with the transcript attached.
func queuePostMessage(ticket *Ticket, transcript *Transcript) discord.Message {
	recovery := button("view", ticket.ID, "Recovery", discordgo.SecondaryButton)
	if transcript == nil {
		message := discord.Signal("ticket", fmt.Sprintf("<@%s> opened a ticket: <#%s>.",
			ticket.OwnerDiscordUserID, ticket.ThreadDiscordChannelID), false)
		message.Components = []discordgo.MessageComponent{discord.Row(
			recovery,
			button("close", ticket.ID, "Close", discordgo.DangerButton),
		)}
		return message
	}
	message := discord.Signal("ticket", fmt.Sprintf("The ticket for <@%s> was closed. The transcript is attached.",
		ticket.OwnerDiscordUserID), false)
	message.Components = []discordgo.MessageComponent{discord.Row(recovery)}
	message.Files = []*discordgo.File{transcriptFile(ticket.ID, transcript)}
	return message
}

// closeNoticeMessage is the member's close DM with their transcript. The
// "Ticket <id>" line is how a retry finds it again.
func closeNoticeMessage(guildName, ticketID string, transcript *Transcript) discord.Message {
	message := discord.Content(fmt.Sprintf(
		"Your ticket in **%s** is closed. Here’s a copy of the conversation for your records.\n-# Ticket %s",
		discord.PlainText(guildName), ticketID), false)
	message.Files = []*discordgo.File{transcriptFile(ticketID, transcript)}
	return message
}

// transcriptFile attaches a transcript as plain text.
func transcriptFile(ticketID string, transcript *Transcript) *discordgo.File {
	return &discordgo.File{
		Name:        transcriptFilename(ticketID),
		ContentType: "text/plain; charset=utf-8",
		Reader:      strings.NewReader(transcript.Content),
	}
}

func transcriptFilename(ticketID string) string { return "ticket-" + ticketID + ".txt" }

// closeComponents is a ticket's Close button.
func closeComponents(ticketID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(button("close", ticketID, "Close", discordgo.DangerButton))}
}

// ticketControls is the Close button plus, for managers, Repair ticket.
func ticketControls(ticketID string, includeRepair bool) []discordgo.MessageComponent {
	if !includeRepair {
		return closeComponents(ticketID)
	}
	return []discordgo.MessageComponent{discord.Row(
		button("close", ticketID, "Close", discordgo.DangerButton),
		button("repair", ticketID, "Repair ticket", discordgo.SecondaryButton),
	)}
}

// finishClosingComponents offers to resume a close that stopped part way.
func finishClosingComponents(ticketID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(button("close", ticketID, "Finish closing", discordgo.SecondaryButton))}
}

// detailPayload splits a view button's payload into the ticket ID and
// history page. A payload without "~page" is page 0, so older buttons keep
// working.
func detailPayload(payload string) (string, int) {
	id, raw, found := strings.Cut(payload, "~")
	if !found {
		return id, 0
	}
	page, err := strconv.Atoi(raw)
	if err != nil || page < 0 {
		return id, 0
	}
	return id, page
}

// historyPages renders a ticket's timeline in pages, keeping each entry
// whole where it fits.
func historyPages(events []Event) []string {
	blocks := make([]string, 0, len(events))
	for _, event := range events {
		body := event.Body
		if body == "" {
			body = string(event.Type)
		}
		blocks = append(blocks, discord.Quote(discord.PlainText(body))+"\n-# "+discord.RelativeTime(event.CreatedAt))
	}
	if len(blocks) == 0 {
		return nil
	}
	return discord.TextPages(strings.Join(blocks, "\n\n"), historyPageSize)
}

// detailMessage is a ticket's private detail view, with only the actions
// that still apply. The caller has authorized actor for the ticket and the
// transcript; queue links are for staff, while the owner gets the retained
// transcript.
func detailMessage(ticket *Ticket, events []Event, actor modules.Actor, pending bool, transcript *Transcript, page int) discord.Message {
	text := fmt.Sprintf("The ticket for <@%s> is **closed**. Closed tickets cannot be reopened.", ticket.OwnerDiscordUserID)
	var components []discordgo.MessageComponent
	switch {
	case ticket.Status == StatusOpen:
		text = fmt.Sprintf("The ticket for <@%s> is **open**.\nOpen the conversation: <#%s>.",
			ticket.OwnerDiscordUserID, ticket.ThreadDiscordChannelID)
		components = ticketControls(ticket.ID, actor.CanManage)
	case pending:
		text += "\nClosing is still in progress. Finish closing to send the transcript and complete thread cleanup."
		components = finishClosingComponents(ticket.ID)
	}
	if ticket.Status == StatusResolved && !pending && actor.CanManage && !ticket.CloseNoticeDelivered {
		text += "\nThe member’s DM isn’t confirmed. You can check delivery and retry if Discord rejected it."
		components = append(components, discord.Row(button("close", ticket.ID, "Retry member DM", discordgo.SecondaryButton)))
	}
	if ticket.Status != StatusOpen {
		if transcript != nil {
			text += "\nThe retained transcript is attached."
		} else {
			text += "\nNo retained transcript is available here."
		}
		if actor.CanModerate && ticket.TranscriptURL != "" {
			text += "\n[View transcript in the staff queue](" + ticket.TranscriptURL + ")."
		}
	}
	if pages := historyPages(events); len(pages) > 0 {
		page = max(0, min(page, len(pages)-1))
		text += fmt.Sprintf("\n\n**Ticket history · %d/%d**\n\n%s", page+1, len(pages), pages[page])
		if len(pages) > 1 {
			pageButton := func(target int, label string, disabled bool) discordgo.Button {
				payload := ticket.ID + "~" + strconv.Itoa(max(0, target))
				return discord.Button(customID("view", payload), label, discordgo.SecondaryButton, disabled)
			}
			components = append(components, discord.Row(
				pageButton(page-1, "Previous", page == 0),
				pageButton(page+1, "Next", page == len(pages)-1),
			))
		}
	}
	message := discord.Signal("ticket", text, true)
	message.Components = components
	if transcript != nil {
		message.Files = []*discordgo.File{transcriptFile(ticket.ID, transcript)}
	}
	return message
}

// existingTicketMessage answers a member who already holds a ticket: where
// it is, or how to finish closing it, without opening another.
func existingTicketMessage(ticket *Ticket) discord.Message {
	if ticket == nil {
		return discord.Signal("ticket", "Your ticket is still opening. Try again in a moment.", true)
	}
	if ticket.Status == StatusOpen {
		message := discord.Signal("ticket", "You already have a ticket: <#"+ticket.ThreadDiscordChannelID+">. Continue the conversation there. If you can’t post there, ask a server administrator to use Repair ticket on this ticket.", true)
		message.Components = closeComponents(ticket.ID)
		return message
	}
	message := discord.Signal("ticket", "Your previous ticket is still being closed. Finish closing it before opening another.", true)
	message.Components = finishClosingComponents(ticket.ID)
	return message
}

// openedMessage answers the member who opened ticket. setupErr is the error
// Open returned with a saved ticket, if any.
func openedMessage(ticket *Ticket, setupErr error) discord.Message {
	if setupErr == nil {
		return discord.Signal("ticket", "Your ticket is ready: <#"+ticket.ThreadDiscordChannelID+">. Type there whenever you’re ready; a moderator will join you.", true)
	}
	// The ticket is saved but its setup did not finish; point the member
	// at recovery rather than letting them open a second ticket.
	message := discord.Signal("ticket", "Your ticket is saved: <#"+ticket.ThreadDiscordChannelID+">, but setup did not finish. If you can’t post there, ask a server administrator to use Repair ticket on this ticket. Opening again will return this same ticket.", true)
	message.Components = []discordgo.MessageComponent{discord.Row(button("view", ticket.ID, "Recovery", discordgo.SecondaryButton))}
	return message
}

// closedCopy describes a close whose transcript is safe.
func closedCopy(ticket *Ticket) string {
	if ticket.CloseNoticeDelivered {
		return "The transcript is saved, and a copy has been DMed to the member. This ticket is closing."
	}
	return "The transcript is saved. I couldn’t confirm a DM to the member. This ticket is closing."
}

// closeFailureMessage describes only progress that was confirmed. Without
// a ticket (it was missing or forbidden) it offers no links or controls.
func closeFailureMessage(ticket *Ticket, err error) discord.Message {
	if ticket == nil {
		return discord.Signal("error", errorMessage(err), true)
	}
	text := "The ticket could not finish closing. Try again; if it keeps failing, ask a server administrator to check Quack's permissions."
	if ticket.Status == StatusResolved {
		text = "The transcript is saved, but cleanup did not finish. Try again to finish closing the ticket."
	}
	message := discord.Signal("error", text, true)
	message.Components = []discordgo.MessageComponent{discord.Row(button("close", ticket.ID, "Retry close", discordgo.SecondaryButton))}
	return message
}

// queueListMessage is the staff list of open tickets, without content.
func queueListMessage(queue []Ticket) discord.Message {
	if len(queue) == 0 {
		return discord.Signal("ticket", "No open tickets right now.", true)
	}
	lines := []string{fmt.Sprintf("You have **%d open tickets** to review.", len(queue))}
	for _, ticket := range queue {
		lines = append(lines, fmt.Sprintf("<#%s> · <@%s> · %s",
			ticket.ThreadDiscordChannelID, ticket.OwnerDiscordUserID, discord.RelativeTime(ticket.CreatedAt)))
	}
	return discord.Signal("ticket", strings.Join(lines, "\n\n"), true)
}

// errorMessage maps an error to copy safe to show the member.
func errorMessage(err error) string {
	switch {
	case errors.Is(err, ErrJournalIncomplete):
		return "This ticket cannot close because some received messages could not be retained. Ask a server administrator to check transcript storage."
	case errors.Is(err, ErrDisabled):
		return "Tickets are not enabled for this server."
	case errors.Is(err, ErrPermissionDenied):
		return "You do not have permission to use that ticket."
	case errors.Is(err, ErrDuplicateOpen):
		return "You already have an open ticket."
	case errors.Is(err, ErrNotFound):
		return "That ticket was not found."
	default:
		return "Something went wrong with this ticket. Try again in a moment."
	}
}

// expectedError reports whether err is a refusal the member caused rather
// than a failure worth an error log.
func expectedError(err error) bool {
	for _, target := range []error{ErrDisabled, ErrPermissionDenied, ErrDuplicateOpen, ErrNotFound,
		ErrInvalidTransition} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
