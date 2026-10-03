package generallogging

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/modules"
)

// ActorResolver returns the caller's current Manage Guild authority.
type ActorResolver func(*http.Request) (Actor, error)

// RegisterRoutes mounts the general logging settings, status, and
// deleted-channel repair routes. Writes need Manage Guild.
func RegisterRoutes(mux modules.Mux, service *Service, resolve ActorResolver) {
	with := func(h func(http.ResponseWriter, *http.Request, Actor)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor, err := resolve(r)
			if err != nil {
				modules.WriteError(w, http.StatusUnauthorized)
				return
			}
			h(w, r, actor)
		})
	}
	canManage := func(r *http.Request) bool {
		actor, err := resolve(r)
		return err == nil && actor.CanManage
	}

	mux.Handle("GET /general-logging/settings", with(func(w http.ResponseWriter, r *http.Request, actor Actor) {
		settings, enabled, status, err := service.Settings(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings, "status": status})
	}))
	mux.Handle("GET /general-logging/status", with(func(w http.ResponseWriter, r *http.Request, actor Actor) {
		_, enabled, status, err := service.Settings(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "status": status})
	}))
	mux.HandleWrite("PUT /general-logging/settings", canManage, with(func(w http.ResponseWriter, r *http.Request, actor Actor) {
		var input struct {
			Enabled  bool     `json:"enabled"`
			Settings Settings `json:"settings"`
		}
		if err := modules.DecodeJSON(r, &input); err != nil {
			modules.WriteError(w, http.StatusBadRequest)
			return
		}
		settings, err := service.UpdateSettings(r.Context(), actor, input.Enabled, input.Settings)
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": input.Enabled, "settings": settings})
	}))
	mux.HandleWrite("POST /general-logging/repair-channel/{channelID}", canManage, with(func(w http.ResponseWriter, r *http.Request, actor Actor) {
		settings, enabled, err := service.RepairDeletedChannel(r.Context(), actor, r.PathValue("channelID"))
		if err != nil {
			writeError(w, err)
			return
		}
		modules.WriteJSON(w, http.StatusOK, map[string]any{"enabled": enabled, "settings": settings})
	}))
}

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
