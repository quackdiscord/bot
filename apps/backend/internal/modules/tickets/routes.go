package tickets

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/quackdiscord/bot/internal/modules"
)

// RegisterRoutes mounts the ticket settings, status, queue, detail,
// transcript, and lifecycle routes. Settings writes need Manage Guild;
// resolving and reopening need a moderator; a ticket's owner may cancel it
// too. Resolve and cancel go through adapter, like the Discord buttons, so
// the transcript is captured and the channel archived whichever surface
// closes the ticket. Reopen changes only Quack's records.
func RegisterRoutes(mux modules.Mux, service *Service, adapter *DiscordAdapter, resolve modules.ActorResolver) {
	h := routes{service: service, adapter: adapter}
	with := func(handle func(http.ResponseWriter, *http.Request, modules.Actor)) http.Handler {
		return modules.WithActor(resolve, handle)
	}
	canManage := modules.Allow(resolve, modules.CanManage)
	canModerate := modules.Allow(resolve, func(actor modules.Actor) bool { return actor.CanModerate })

	mux.Handle("GET /tickets/settings", with(h.settings))
	mux.Handle("GET /tickets/status", with(h.status))
	mux.HandleWrite("PUT /tickets/settings", canManage, with(h.updateSettings))
	mux.Handle("GET /tickets/queue", with(h.queue))
	mux.Handle("GET /tickets/{ticketID}", with(h.detail))
	mux.Handle("GET /tickets/{ticketID}/transcript", with(h.transcript))
	mux.HandleWrite("POST /tickets/{ticketID}/resolve", canModerate, with(h.resolve))
	mux.HandleWrite("POST /tickets/{ticketID}/cancel", allowCancel(service, resolve), with(h.cancel))
	mux.HandleWrite("POST /tickets/{ticketID}/reopen", canModerate, with(h.reopen))
}

// routes are the ticket HTTP handlers.
type routes struct {
	service *Service
	adapter *DiscordAdapter
}

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings})
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	status, err := rt.service.Status(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (rt routes) updateSettings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	var input struct {
		Enabled  bool     `json:"enabled"`
		Settings Settings `json:"settings"`
	}
	if err := modules.DecodeJSON(r, &input); err != nil {
		modules.WriteError(w, http.StatusBadRequest)
		return
	}
	settings, err := rt.service.UpdateSettings(r.Context(), actor, input.Enabled, input.Settings)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled, "settings": settings})
}

func (rt routes) queue(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	tickets, err := rt.service.Queue(r.Context(), actor, Status(query.Get("status")), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (rt routes) detail(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	ticket, events, err := rt.service.Detail(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "events": events})
}

func (rt routes) transcript(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	transcript, err := rt.service.Transcript(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"transcript": transcript})
}

func (rt routes) resolve(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	// The body must still be JSON, as before, but the transcript is now
	// captured from Discord, so a "transcript" field is ignored.
	var input struct{}
	if err := modules.DecodeJSON(r, &input); err != nil {
		modules.WriteError(w, http.StatusBadRequest)
		return
	}
	ticket, err := rt.adapter.Close(r.Context(), actor, r.PathValue("ticketID"))
	writeTicket(w, ticket, err)
}

func (rt routes) cancel(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	ticket, err := rt.adapter.Cancel(r.Context(), actor, r.PathValue("ticketID"))
	writeTicket(w, ticket, err)
}

func (rt routes) reopen(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	ticket, err := rt.service.Reopen(r.Context(), actor, r.PathValue("ticketID"))
	writeTicket(w, ticket, err)
}

// allowCancel lets moderators and the ticket's owner cancel it. A ticket the
// caller cannot see is refused here too, so its existence does not leak.
func allowCancel(service *Service, resolve modules.ActorResolver) func(*http.Request) bool {
	return func(r *http.Request) bool {
		actor, err := resolve(r)
		if err != nil {
			return false
		}
		_, err = service.visibleTicket(r.Context(), actor, r.PathValue("ticketID"))
		return err == nil
	}
}

// writeTicket writes a lifecycle result.
func writeTicket(w http.ResponseWriter, ticket *Ticket, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

// writeError maps a service error to its status code.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		modules.WriteError(w, http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		modules.WriteError(w, http.StatusNotFound)
	case errors.Is(err, ErrDuplicateOpen), errors.Is(err, ErrInvalidTransition):
		modules.WriteError(w, http.StatusConflict)
	case errors.Is(err, ErrRateLimited):
		modules.WriteError(w, http.StatusTooManyRequests)
	case errors.Is(err, ErrDisabled):
		modules.WriteError(w, http.StatusServiceUnavailable)
	case errors.Is(err, ErrDiscord):
		modules.WriteError(w, http.StatusBadGateway)
	default:
		modules.WriteError(w, http.StatusBadRequest)
	}
}
