package moduleintegration

import (
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// RegisterComponents installs ticket buttons and the reply modal on the
// process's interaction router. The /case and appeal controls are installed
// by discord.NewRouter.
func (r *Runtime) RegisterComponents(router *discord.Router) error {
	if r == nil || r.TicketDiscord == nil || r.Tickets == nil {
		return errors.New("ticket Discord runtime is not configured")
	}
	handlers := tickets.ComponentHandlers{
		Open: r.openTicketComponent, Queue: r.ticketQueueComponent,
		View: r.viewTicketComponent, Reply: r.replyTicketComponent,
		Close: r.closeTicketComponent,
	}
	if err := tickets.RegisterComponents(router, handlers); err != nil {
		return err
	}
	router.HandleComponent("ticket", "repair", r.repairTicketComponent)
	router.HandleModal("ticket", "reply-submit", r.submitTicketReplyModal)
	return nil
}

// RegisterGatewayHandlers subscribes optional modules to gateway events without
// placing their failures on the moderation action queue.
func (r *Runtime) RegisterGatewayHandlers(session *discordgo.Session) error {
	if r == nil || r.LoggingQueue == nil || r.HoneypotRuntime == nil || r.HoneypotDiscord == nil || session == nil {
		return errors.New("optional module gateway runtime is not configured")
	}
	session.AddHandler(r.onMessageCreate)
	session.AddHandler(r.onMessageUpdate)
	session.AddHandler(r.onMessageDelete)
	session.AddHandler(r.onMessageDeleteBulk)
	session.AddHandler(r.onGuildMemberAdd)
	session.AddHandler(r.onGuildMemberRemove)
	session.AddHandler(r.onTicketGuildCreate)
	session.AddHandler(r.onTicketMemberUpdate)
	session.AddHandler(r.onTicketRoleUpdate)
	session.AddHandler(r.onTicketRoleDelete)
	session.AddHandler(r.onGuildBanAdd)
	session.AddHandler(r.onGuildBanRemove)
	session.AddHandler(r.onGuildUpdate)
	session.AddHandler(r.onGuildDelete)
	session.AddHandler(r.onChannelCreate)
	session.AddHandler(r.onChannelUpdate)
	session.AddHandler(r.onChannelDelete)
	return nil
}
