package moduleintegration

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/api"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

var errNoGuildContext = errors.New("live guild context is unavailable")

// MountHTTP mounts the three modules' routes on the API.
func (r *Runtime) MountHTTP(mux *api.ModuleMux) {
	tickets.RegisterRoutes(mux, r.Tickets, resolveTicketActor)
	generallogging.RegisterRoutes(mux, r.Logging, resolveLoggingActor)
	honeypot.RegisterRoutes(mux, r.Honeypot, resolveHoneypotActor)
}

// resolveActor maps the request's live guild context, set by the API's guild
// middleware, to a module actor.
func resolveActor[A any](r *http.Request, actor func(*quack.GuildStaffContext) A) (A, error) {
	staff := api.GuildStaff(r.Context())
	if staff == nil || staff.Guild == nil {
		var zero A
		return zero, errNoGuildContext
	}
	return actor(staff), nil
}

func resolveTicketActor(r *http.Request) (tickets.Actor, error) {
	return resolveActor(r, ticketActor)
}

func resolveLoggingActor(r *http.Request) (generallogging.Actor, error) {
	return resolveActor(r, loggingActor)
}

func resolveHoneypotActor(r *http.Request) (honeypot.Actor, error) {
	return resolveActor(r, honeypotActor)
}

// ticketActor grants ticket management to Manage Guild and ticket
// moderation to whoever can resolve tickets.
func ticketActor(staff *quack.GuildStaffContext) tickets.Actor {
	return tickets.Actor{
		GuildID:       staff.Guild.ID,
		DiscordUserID: staff.ActorDiscordUserID,
		CanManage:     staff.Can(quack.PermissionActionGuildSettingsWrite),
		CanModerate:   staff.Can(quack.PermissionActionTicketResolve),
	}
}

func loggingActor(staff *quack.GuildStaffContext) generallogging.Actor {
	return generallogging.Actor{
		GuildID:       staff.Guild.ID,
		DiscordUserID: staff.ActorDiscordUserID,
		CanManage:     staff.Can(quack.PermissionActionGuildSettingsWrite),
	}
}

func honeypotActor(staff *quack.GuildStaffContext) honeypot.Actor {
	return honeypot.Actor{
		GuildID:       staff.Guild.ID,
		DiscordUserID: staff.ActorDiscordUserID,
		CanManage:     staff.Can(quack.PermissionActionGuildSettingsWrite),
	}
}
