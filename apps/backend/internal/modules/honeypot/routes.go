package honeypot

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
)

// RegisterRoutes mounts the honeypot settings, status, and repair routes.
// Every route needs Manage Guild; writes are also refused before they run.
func RegisterRoutes(mux modules.Mux, service *Service, resolve modules.ActorResolver) {
	h := routes{service: service}
	with := func(handle func(http.ResponseWriter, *http.Request, modules.Actor)) http.Handler {
		return modules.WithActor(resolve, handle)
	}
	canManage := modules.Allow(resolve, modules.CanManage)

	mux.Handle("GET /honeypot/settings", with(h.settings))
	mux.Handle("GET /honeypot/status", with(h.status))
	mux.HandleWrite("PUT /honeypot/settings", canManage, with(h.updateSettings))
	mux.HandleWrite("POST /honeypot/repair", canManage, with(h.repair))
}

// routes are the honeypot HTTP handlers.
type routes struct{ service *Service }

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, status, err := rt.service.Settings(r.Context(), actor)
	writeSettings(w, settings, status, err)
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	_, status, err := rt.service.Settings(r.Context(), actor)
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
	settings, status, err := rt.service.UpdateSettings(r.Context(), actor, input.Enabled, input.Settings)
	writeSettings(w, settings, status, err)
}

func (rt routes) repair(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, status, err := rt.service.Repair(r.Context(), actor)
	writeSettings(w, settings, status, err)
}

// writeSettings writes a settings result.
func writeSettings(w http.ResponseWriter, settings Settings, status Status, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, map[string]any{"settings": settings, "status": status})
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
