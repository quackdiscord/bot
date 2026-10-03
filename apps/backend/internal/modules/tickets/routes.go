package tickets

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/quackdiscord/bot/internal/modules"
)

// Closer closes a ticket the way the Discord Close button does: transcript
// captured and published, member notified, thread deleted. *DiscordAdapter
// implements it.
type Closer interface {
	Close(ctx context.Context, actor modules.Actor, ticketID string) (*Ticket, error)
}

// RegisterRoutes mounts the ticket settings, status, queue, detail,
// transcript, and close routes. Settings writes need Manage Guild. The
// owner or a moderator may close; resolve and cancel are older names for
// close, and none of them can skip the transcript or thread cleanup.
// Closed tickets cannot be reopened, so reopen always answers 409.
func RegisterRoutes(mux modules.Mux, service *Service, closer Closer, resolve modules.ActorResolver) {
	h := routes{service: service, closer: closer}
	with := func(handle func(http.ResponseWriter, *http.Request, modules.Actor)) http.Handler {
		return modules.WithActor(resolve, handle)
	}
	canManage := modules.Allow(resolve, modules.CanManage)
	canClose := allowClose(service, resolve)

	mux.Handle("GET /tickets/settings", with(h.settings))
	mux.Handle("GET /tickets/status", with(h.status))
	mux.HandleWrite("PUT /tickets/settings", canManage, with(h.updateSettings))
	mux.Handle("GET /tickets/queue", with(h.queue))
	mux.Handle("GET /tickets/{ticketID}", with(h.detail))
	mux.Handle("GET /tickets/{ticketID}/transcript", with(h.transcript))
	mux.HandleWrite("POST /tickets/{ticketID}/close", canClose, with(h.close))
	mux.HandleWrite("POST /tickets/{ticketID}/resolve", canClose, with(h.close))
	mux.HandleWrite("POST /tickets/{ticketID}/cancel", canClose, with(h.close))
	mux.HandleWrite("POST /tickets/{ticketID}/reopen", canClose, with(h.reopen))
}

// routes are the ticket HTTP handlers.
type routes struct {
	service *Service
	closer  Closer
}

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings})
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	status, err := rt.service.Status(r.Context(), actor)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
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
		writeError(w, err, http.StatusBadRequest)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled, "settings": settings})
}

func (rt routes) queue(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	tickets, err := rt.service.Queue(r.Context(), actor, Status(query.Get("status")), limit)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"tickets": tickets})
}

func (rt routes) detail(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	ticket, events, err := rt.service.Detail(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "events": events})
}

func (rt routes) transcript(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	transcript, err := rt.service.Transcript(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"transcript": transcript})
}

// close closes the ticket. Any request body, such as an old client's
// "transcript" field, is ignored: the transcript always comes from Discord.
func (rt routes) close(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	ticket, err := rt.closer.Close(r.Context(), actor, r.PathValue("ticketID"))
	if err != nil {
		// Anything unexpected failed in Discord or storage part way
		// through; the ticket's state says how far it got.
		writeError(w, err, http.StatusBadGateway)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"ticket": ticket})
}

func (rt routes) reopen(w http.ResponseWriter, _ *http.Request, _ modules.Actor) {
	writeError(w, ErrInvalidTransition, http.StatusConflict)
}

// allowClose lets moderators and the ticket's owner close it. A ticket the
// caller cannot see is refused here too, so its existence does not leak.
func allowClose(service *Service, resolve modules.ActorResolver) func(*http.Request) bool {
	return func(r *http.Request) bool {
		actor, err := resolve(r)
		if err != nil {
			return false
		}
		_, err = service.visibleTicket(r.Context(), actor, r.PathValue("ticketID"))
		return err == nil
	}
}

// writeError maps a service error to its status code, or fallback.
func writeError(w http.ResponseWriter, err error, fallback int) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		modules.WriteError(w, http.StatusForbidden)
	case errors.Is(err, ErrNotFound):
		modules.WriteError(w, http.StatusNotFound)
	case errors.Is(err, ErrDuplicateOpen), errors.Is(err, ErrInvalidTransition),
		errors.Is(err, ErrQueueDeliveryUnknown), errors.Is(err, ErrJournalIncomplete):
		modules.WriteError(w, http.StatusConflict)
	case errors.Is(err, ErrDisabled):
		modules.WriteError(w, http.StatusServiceUnavailable)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		modules.WriteError(w, http.StatusServiceUnavailable)
	default:
		modules.WriteError(w, fallback)
	}
}
