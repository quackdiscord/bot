package routes

import "github.com/go-chi/chi/v5"

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/ping", ping)

	return r
}
