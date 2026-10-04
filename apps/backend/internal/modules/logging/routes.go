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

	failures := []int{http.StatusBadRequest, http.StatusForbidden, http.StatusServiceUnavailable}

	mux.Handle("GET /general-logging/settings", modules.Doc{
		ID: "getLoggingSettings", Summary: "General logging settings and health",
		Response: settingsResponse{}, Errors: failures,
	}, with(h.settings))
	mux.Handle("GET /general-logging/status", modules.Doc{
		ID: "getLoggingStatus", Summary: "General logging health",
		Response: statusResponse{}, Errors: failures,
	}, with(h.status))
	mux.HandleWrite("PUT /general-logging/settings", modules.Doc{
		ID: "updateLoggingSettings", Summary: "Replace general logging settings; needs Manage Guild",
		Body: settingsPayload{}, Response: settingsPayload{}, Errors: failures,
	}, canManage, with(h.updateSettings))
	mux.HandleWrite("POST /general-logging/repair-channel/{channelID}", modules.Doc{
		ID: "repairLoggingChannel", Summary: "Remove every route to a deleted channel; needs Manage Guild",
		Response: settingsPayload{}, Errors: failures,
	}, canManage, with(h.repairChannel))
}

// settingsPayload is whether general logging is on and its settings, as
// read and as written back. A write replaces both.
type settingsPayload struct {
	Enabled  bool     `json:"enabled"`
	Settings Settings `json:"settings"`
}

// settingsResponse is the settings with the module's health.
type settingsResponse struct {
	Enabled  bool     `json:"enabled"`
	Settings Settings `json:"settings"`
	Status   Status   `json:"status"`
}

type statusResponse struct {
	Enabled bool   `json:"enabled"`
	Status  Status `json:"status"`
}

// routes are the general logging HTTP handlers.
type routes struct{ service *Service }

func (rt routes) settings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, status, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, settingsResponse{Enabled: enabled, Settings: settings, Status: status})
}

func (rt routes) status(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	_, enabled, status, err := rt.service.Settings(r.Context(), actor)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, statusResponse{Enabled: enabled, Status: status})
}

func (rt routes) updateSettings(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	var input settingsPayload
	if err := modules.DecodeJSON(r, &input); err != nil {
		modules.WriteError(w, http.StatusBadRequest)
		return
	}
	settings, err := rt.service.UpdateSettings(r.Context(), actor, input.Enabled, input.Settings)
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, settingsPayload{Enabled: input.Enabled, Settings: settings})
}

func (rt routes) repairChannel(w http.ResponseWriter, r *http.Request, actor modules.Actor) {
	settings, enabled, err := rt.service.RepairDeletedChannel(r.Context(), actor, r.PathValue("channelID"))
	if err != nil {
		writeError(w, err)
		return
	}
	modules.WriteJSON(w, http.StatusOK, settingsPayload{Enabled: enabled, Settings: settings})
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
