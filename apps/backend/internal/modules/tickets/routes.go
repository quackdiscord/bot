package tickets

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/quackdiscord/bot/internal/modules"
)

// ActorResolver returns the caller's current ticket authority.
type ActorResolver func(*http.Request) (Actor, error)

// RegisterRoutes mounts the ticket settings, status, queue, detail,
// transcript, and lifecycle routes. Settings writes need Manage Guild;
// resolving and reopening need moderation rights; a ticket's owner may
// cancel it too.
func RegisterRoutes(mux modules.Mux, service *Service, resolve ActorResolver) {
	h := routes{service: service, resolve: resolve}
	mux.Handle("GET /tickets/settings", h.with(h.settings))
	mux.Handle("GET /tickets/status", h.with(h.status))
	mux.HandleWrite("PUT /tickets/settings", h.allow(canManage), h.with(h.updateSettings))
	mux.Handle("GET /tickets/queue", h.with(h.queue))
	mux.Handle("GET /tickets/{ticketID}", h.with(h.detail))
	mux.Handle("GET /tickets/{ticketID}/transcript", h.with(h.transcript))
	mux.HandleWrite("POST /tickets/{ticketID}/resolve", h.allow(canModerate), h.with(h.resolveTicket))
	mux.HandleWrite("POST /tickets/{ticketID}/cancel", h.allowCancel, h.with(h.cancel))
	mux.HandleWrite("POST /tickets/{ticketID}/reopen", h.allow(canModerate), h.with(h.reopen))
}

type routes struct {
	service *Service
	resolve ActorResolver
}

func canManage(actor Actor) bool   { return actor.CanManage }
func canModerate(actor Actor) bool { return actor.CanModerate }

// with resolves the actor before calling h.
func (rt routes) with(h func(http.ResponseWriter, *http.Request, Actor)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := rt.resolve(r)
		if err != nil {
			modules.WriteError(w, http.StatusUnauthorized)
			return
		}
		h(w, r, actor)
	})
}

func (rt routes) allow(check func(Actor) bool) func(*http.Request) bool {
	return func(r *http.Request) bool {
		actor, err := rt.resolve(r)
		return err == nil && check(actor)
	}
}

// allowCancel lets moderators and the ticket's owner cancel it.
func (rt routes) allowCancel(r *http.Request) bool {
	actor, err := rt.resolve(r)
	if err != nil {
		return false
	}
	ticket, _, err := rt.service.Detail(r.Context(), actor, r.PathValue("ticketID"))
	return err == nil && ticket != nil && (actor.CanModerate || ticket.OwnerDiscordUserID == actor.DiscordUserID)
}

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor Actor) {
	settings, enabled, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings})
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor Actor) {
	status, err := rt.service.Status(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (rt routes) updateSettings(w http.ResponseWriter, r *http.Request, actor Actor) {
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

func (rt routes) queue(w http.ResponseWriter, r *http.Request, actor Actor) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, err := rt.service.Queue(r.Context(), actor, Status(query.Get("status")), limit)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"tickets": items})
}

func (rt routes) detail(w http.ResponseWriter, r *http.Request, actor Actor) {
	ticket, events, err := rt.service.Detail(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "events": events})
}

func (rt routes) transcript(w http.ResponseWriter, r *http.Request, actor Actor) {
	transcript, err := rt.service.Transcript(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"transcript": transcript})
}

func (rt routes) resolveTicket(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input struct {
		Transcript string `json:"transcript"`
	}
	if err := modules.DecodeJSON(r, &input); err != nil {
		modules.WriteError(w, http.StatusBadRequest)
		return
	}
	ticket, err := rt.service.Resolve(r.Context(), actor, r.PathValue("ticketID"), input.Transcript)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (rt routes) cancel(w http.ResponseWriter, r *http.Request, actor Actor) {
	ticket, err := rt.service.Cancel(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (rt routes) reopen(w http.ResponseWriter, r *http.Request, actor Actor) {
	ticket, err := rt.service.Reopen(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

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
	default:
		modules.WriteError(w, http.StatusBadRequest)
	}
}
