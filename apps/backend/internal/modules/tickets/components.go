package tickets

import (
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
)

const componentNamespace = "ticket"

// ComponentHandlers supplies integration-owned identity resolution and presentation for ticket interactions.
type ComponentHandlers struct {
	Open, Queue, View, Reply, Close discord.Handler
}

// ComponentRouter is where ticket buttons are installed; *discord.Router
// satisfies it.
type ComponentRouter interface {
	HandleComponent(namespace, action string, handler discord.Handler)
}

// RegisterComponents installs the complete ticket interaction surface on router.
func RegisterComponents(router ComponentRouter, handlers ComponentHandlers) error {
	entries := []struct {
		name    string
		handler discord.Handler
	}{{"open", handlers.Open}, {"queue", handlers.Queue}, {"view", handlers.View}, {"reply", handlers.Reply}, {"close", handlers.Close}}
	for _, entry := range entries {
		if entry.handler == nil {
			return errors.New("all ticket component handlers are required")
		}
	}
	for _, entry := range entries {
		router.HandleComponent(componentNamespace, entry.name, entry.handler)
	}
	return nil
}

// EntryComponents builds member-open and staff-queue controls with stable routed identifiers.
func EntryComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(
		discord.Button(discord.MustCustomID(discord.CustomID{Namespace: componentNamespace, Action: "open", Version: "v1"}), "Open ticket", discordgo.PrimaryButton, false),
		discord.Button(discord.MustCustomID(discord.CustomID{Namespace: componentNamespace, Action: "queue", Version: "v1"}), "Staff queue", discordgo.SecondaryButton, false),
	)}
}

// TicketComponents builds private view, reply, and close controls for one ticket.
func TicketComponents(ticketID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discord.Row(
		discord.Button(discord.MustCustomID(discord.CustomID{Namespace: componentNamespace, Action: "view", Version: "v1", Payload: ticketID}), "View", discordgo.SecondaryButton, false),
		discord.Button(discord.MustCustomID(discord.CustomID{Namespace: componentNamespace, Action: "reply", Version: "v1", Payload: ticketID}), "Reply", discordgo.PrimaryButton, false),
		discord.Button(discord.MustCustomID(discord.CustomID{Namespace: componentNamespace, Action: "close", Version: "v1", Payload: ticketID}), "Close", discordgo.DangerButton, false),
	)}
}
