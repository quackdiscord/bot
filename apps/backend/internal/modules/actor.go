package modules

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// Actor is the caller of a module operation and what they may do, taken
// from live Discord permissions.
type Actor struct {
	GuildID, DiscordUserID string
	// CanManage is Manage Guild: configure the module and repair it.
	CanManage bool
	// CanModerate is the ticket.resolve permission: work the ticket queue.
	CanModerate bool
}

// ActorFor maps a live staff context to a module actor.
func ActorFor(staff *quack.GuildStaffContext) Actor {
	return Actor{
		GuildID:       staff.Guild.ID,
		DiscordUserID: staff.ActorDiscordUserID,
		CanManage:     staff.Can(quack.PermissionActionGuildSettingsWrite),
		CanModerate:   staff.Can(quack.PermissionActionTicketResolve),
	}
}

// ActorResolver returns the actor behind an HTTP request. Module routes take
// one so tests can supply an actor without the API's session middleware.
type ActorResolver func(*http.Request) (Actor, error)

// RequestActor is the ActorResolver for routes mounted on api.ModuleMux,
// whose guild middleware has already put live staff context on the request.
func RequestActor(r *http.Request) (Actor, error) {
	staff := quack.StaffFromContext(r.Context())
	if staff == nil || staff.Guild == nil {
		return Actor{}, errors.New("live guild context is unavailable")
	}
	return ActorFor(staff), nil
}
