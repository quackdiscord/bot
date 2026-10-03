package modules

import (
	"encoding/json"
	"net/http"
)

// Mux is where a module mounts its HTTP routes. Patterns are a method and a
// path relative to the guild's module prefix, such as "GET /tickets/status".
// api.ModuleMux implements it, adding authentication, live guild context,
// rate limits, and idempotent writes.
type Mux interface {
	Handle(pattern string, h http.Handler)
	// HandleWrite mounts a write that only callers passing allowed may make.
	HandleWrite(pattern string, allowed func(*http.Request) bool, h http.Handler)
}

// WithActor adapts a handler that needs the caller's actor. A request whose
// actor cannot be resolved gets a 401.
func WithActor(resolve ActorResolver, h func(http.ResponseWriter, *http.Request, Actor)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, err := resolve(r)
		if err != nil {
			WriteError(w, http.StatusUnauthorized)
			return
		}
		h(w, r, actor)
	})
}

// Allow turns an actor check into the allowed func HandleWrite takes.
func Allow(resolve ActorResolver, check func(Actor) bool) func(*http.Request) bool {
	return func(r *http.Request) bool {
		actor, err := resolve(r)
		return err == nil && check(actor)
	}
}

// CanManage reports whether the actor has Manage Guild. It is the check
// most module writes pass to Allow.
func CanManage(actor Actor) bool { return actor.CanManage }

// WriteJSON writes v as a JSON response.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// WriteError writes a bare error status. The API replaces the body with its
// error envelope, so module error text never reaches clients.
func WriteError(w http.ResponseWriter, status int) {
	w.WriteHeader(status)
}

// DecodeJSON decodes one JSON value from the request body. Unlike the core
// API it ignores unknown fields, as the module routes always have.
func DecodeJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}
