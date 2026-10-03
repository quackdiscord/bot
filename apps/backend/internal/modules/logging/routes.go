package logging

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
)

// RegisterRoutes mounts the general logging settings, status, and
// deleted-channel repair routes. Writes need Manage Guild.
func RegisterRoutes(mux modules.Mux, service *Service, resolve modules.ActorResolver) {
	h := routes{service: service}
	with := func(handle func(http.ResponseWriter, *http.Request, modules.Actor)) http.Handler {
		return modules.WithActor(resolve, handle)
	}
	canManage := modules.Allow(resolve, modules.CanManage)

	mux.Handle("GET /general-logging/settings", with(h.settings))
	mux.Handle("GET /general-logging/status", with(h.status))
	mux.HandleWrite("PUT /general-logging/settings", canManage, with(h.updateSettings))
	mux.HandleWrite("POST /general-logging/repair-channel/{channelID}", canManage, with(h.repairChannel))
}

// routes are the general logging HTTP handlers.
type routes struct{ service *Service }

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, status, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings, "status": status})
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	_, enabled, status, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "status": status})
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

func (rt routes) repairChannel(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, err := rt.service.RepairDeletedChannel(r.Context(), actor, r.PathValue("channelID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings})
}

// writeError maps a service error to its status code.
func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrDisabled):
		modules.WriteError(w, http.StatusServiceUnavailable)
	case errors.Is(err, ErrPermissionDenied):
		modules.WriteError(w, http.StatusForbidden)
	default:
		modules.WriteError(w, http.StatusBadRequest)
	}
}
