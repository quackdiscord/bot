package api

import (
	"net/http"
	"slices"
	"strings"
)

// Doc describes one route for the HTTP contract,
// contracts/http/openapi.yaml. Every route passes its Doc to the call that
// mounts it, so the contract is generated from the same table that builds
// the mux and the two cannot disagree. Query, Body, Response, and Also
// bodies are zero values of the exact types the handler reads and writes.
//
// Those types carry a few tags for the contract alone: nullable:"false" on
// a pointer, slice, or map the handler never leaves nil; type:"integer" (with
// minimum, maximum, default) on a query string that holds a number; and
// format or enum where a string is constrained.
type Doc struct {
	// ID is the operation ID client generators name functions after. It is
	// unique across the API.
	ID      string
	Summary string
	// Description adds detail the summary cannot carry, or is empty.
	Description string
	// Query is a struct whose `query` tags name the query parameters, or nil.
	Query any
	// Body is the JSON request body, or nil for none.
	Body any
	// Status is the success status; zero means 200.
	Status int
	// Response is the success body, or nil for none.
	Response any
	// ContentType is the success body's media type; empty means JSON.
	ContentType string
	// Also lists other responses, such as a redirect's JSON mode. An error
	// status listed here keeps the body the handler writes instead of being
	// rewritten into the error envelope, as /readyz does for its report.
	Also []Response
	// Errors are the error statuses the handler itself can answer with.
	// Route.Errors adds the ones its protection can.
	Errors []int
}

// Response is one documented non-error response.
type Response struct {
	Status      int
	Description string
	// Body is the JSON body, or nil for none.
	Body any
}

// Auth is the credential a route accepts.
type Auth int

const (
	// AuthNone is a public route.
	AuthNone Auth = iota
	// AuthSession takes the session cookie or a bearer session token.
	AuthSession
	// AuthMetricsKey takes the X-Quack-Metrics-Key header.
	AuthMetricsKey
	// AuthOpsKey takes the X-Quack-Ops-Key header.
	AuthOpsKey
	// AuthOpsKeyOrSession takes the ops key, or else a session.
	AuthOpsKeyOrSession
)

// Protection is what a route's middleware enforces, as far as a client can
// tell. The registration helpers in routes.go set it alongside the
// middleware itself.
type Protection struct {
	Auth Auth
	// RateLimited routes can answer 429, and 503 while Redis is down.
	RateLimited bool
	// Guild routes resolve the caller's live permissions in
	// {discordGuildID}.
	Guild bool
	// Idempotent routes require an Idempotency-Key header.
	Idempotent bool
}

// Route is one mounted route as the contract generator sees it.
type Route struct {
	Method string
	// Path is the ServeMux path pattern, such as "/guilds/{discordGuildID}".
	Path string
	Protection
	Doc Doc
}

// Routes returns every route the server has mounted, core and module, in
// registration order.
func (s *Server) Routes() []Route {
	return slices.Clone(s.table)
}

// ErrorBody returns a zero value of the error envelope that every error
// status carries.
func ErrorBody() any {
	return errorResponse{}
}

// ErrorStatuses returns every error status the route documents: the
// handler's own, plus those its protection can answer with. Any status can
// also be a 500.
func (r Route) ErrorStatuses() []int {
	statuses := slices.Clone(r.Doc.Errors)
	if r.RateLimited {
		statuses = append(statuses, http.StatusTooManyRequests, http.StatusServiceUnavailable)
	}
	switch r.Auth {
	case AuthSession, AuthOpsKeyOrSession:
		statuses = append(statuses, http.StatusUnauthorized, http.StatusServiceUnavailable)
		if isWrite(r.Method) {
			// csrf and cors reject cookie writes from elsewhere.
			statuses = append(statuses, http.StatusForbidden)
		}
	case AuthMetricsKey, AuthOpsKey:
		// A wrong key is a 403; no key configured is a 404.
		statuses = append(statuses, http.StatusForbidden, http.StatusNotFound)
	}
	if r.Guild {
		statuses = append(statuses, http.StatusForbidden, http.StatusNotFound)
	}
	if r.Idempotent {
		statuses = append(statuses, http.StatusBadRequest, http.StatusConflict, http.StatusServiceUnavailable)
	}
	slices.Sort(statuses)
	return slices.Compact(statuses)
}

// mount registers h for pattern and records the route for the contract. It
// is the only place routes reach the mux.
func (s *Server) mount(pattern string, p Protection, d Doc, h http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	s.mux.Handle(pattern, h)
	s.table = append(s.table, Route{Method: method, Path: path, Protection: p, Doc: d})
	for _, also := range d.Also {
		if also.Status >= http.StatusBadRequest && also.Body != nil {
			if s.ownErrorBodies == nil {
				s.ownErrorBodies = map[string][]int{}
			}
			s.ownErrorBodies[pattern] = append(s.ownErrorBodies[pattern], also.Status)
		}
	}
}
