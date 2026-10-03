package honeypot

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
)

// RegisterRoutes mounts the honeypot settings, status, and repair routes.
// Writes need Manage Guild.
func RegisterRoutes(mux modules.Mux, service *Service, resolve modules.ActorResolver) {
	with := func(h func(http.ResponseWriter, *http.Request, modules.Actor)) http.Handler {
		return modules.WithActor(resolve, h)
	}
	canManage := modules.Allow(resolve, modules.CanManage)

	mux.Handle("GET /honeypot/settings", with(func(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
		settings, status, err := service.Settings(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"settings": settings, "status": status})
	}))
	mux.Handle("GET /honeypot/status", with(func(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
		_, status, err := service.Settings(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"status": status})
	}))
	mux.HandleWrite("PUT /honeypot/settings", canManage, with(func(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
		var input struct {
			Enabled  bool     `json:"enabled"`
			Settings Settings `json:"settings"`
		}
		if err := modules.DecodeJSON(r, &input); err != nil {
			modules.WriteError(w, http.StatusBadRequest)
			return
		}
		settings, status, err := service.UpdateSettings(r.Context(), actor, input.Enabled, input.Settings)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"settings": settings, "status": status})
	}))
	mux.HandleWrite("POST /honeypot/repair", canManage, with(func(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
		settings, status, err := service.Repair(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"settings": settings, "status": status})
	}))
}

// writeError maps a service error to its status code.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrPermissionDenied):
		modules.WriteError(w, http.StatusForbidden)
	case errors.Is(err, ErrDisabled), errors.Is(err, ErrChannelUnavailable), errors.Is(err, ErrTemplateUnavailable):
		modules.WriteError(w, http.StatusServiceUnavailable)
	default:
		modules.WriteError(w, http.StatusBadRequest)
	}
}
