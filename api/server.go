package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/quackdiscord/bot/api/middleware"
	"github.com/quackdiscord/bot/api/routes"
	"github.com/quackdiscord/bot/services"
	"github.com/rs/zerolog/log"
)

func Start() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Mount("/", routes.Router())

	log.Info().Msg("API server started on port 8080")
	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Error().Err(err).Msg("Failed to start API server")
		services.CaptureError(err)
	}
}
