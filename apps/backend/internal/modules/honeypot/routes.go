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

	failures := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable}

	mux.Handle("GET /honeypot/settings", modules.Doc{
		ID: "getHoneypotSettings", Summary: "Honeypot settings and health; needs Manage Guild",
		Response: settingsResponse{}, Errors: failures,
	}, with(h.settings))
	mux.Handle("GET /honeypot/status", modules.Doc{
		ID: "getHoneypotStatus", Summary: "Honeypot health; needs Manage Guild",
		Response: statusResponse{}, Errors: failures,
	}, with(h.status))
	mux.HandleWrite("PUT /honeypot/settings", modules.Doc{
		ID: "updateHoneypotSettings", Summary: "Replace honeypot settings; needs Manage Guild",
		Body: settingsRequest{}, Response: settingsResponse{}, Errors: failures,
	}, canManage, with(h.updateSettings))
	mux.HandleWrite("POST /honeypot/repair", modules.Doc{
		ID: "repairHoneypot", Summary: "Turn the honeypot back on with its kept settings, checked live; needs Manage Guild",
		Response: settingsResponse{}, Errors: failures,
	}, canManage, with(h.repair))
}

// settingsRequest turns the honeypot on or off and replaces its settings.
type settingsRequest struct {
	Enabled  bool     `json:"enabled"`
	Settings Settings `json:"settings"`
}

// settingsResponse is the honeypot's settings and health.
type settingsResponse struct {
	Settings Settings `json:"settings"`
	Status   Status   `json:"status"`
}

type statusResponse struct {
	Status Status `json:"status"`
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
	modules.WriteJSON(w, http.StatusOK, statusResponse{Status: status})
}

func (rt routes) updateSettings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	var input settingsRequest
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
	modules.WriteJSON(w, http.StatusOK, settingsResponse{Settings: settings, Status: status})
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
