// Package api is the dashboard's HTTP API, served with net/http.
//
// A request passes through one global pipeline (trace IDs, the error
// envelope, security headers, CORS, body limit, CSRF) and is then routed.
// Guild and member routes add, in order: the endpoint rate limit, the session,
// live guild authorization, and for writes an Idempotency-Key. routes.go
// lists every route. Handlers decode the request, call one quack service,
// and encode the result; service errors become statuses through the tables
// in json.go. The JSON contract is fixed by the dashboard; every route
// registers a Doc, from which internal/contract generates
// contracts/http/openapi.yaml.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// Storage is the storage the API uses directly, outside the domain services:
// sessions and OAuth state, health checks, and metrics.
type Storage interface {
	SaveOAuthState(ctx context.Context, stateID string, state *quack.OAuthState, ttl time.Duration) error
	ConsumeOAuthState(ctx context.Context, stateID string) (*quack.OAuthState, error)
	SaveSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) error
	GetSession(ctx context.Context, sessionID string) (*quack.AuthSession, error)
	RefreshSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) (bool, error)
	DeleteSession(ctx context.Context, sessionID string) error
	RevokeUserSessions(ctx context.Context, discordUserID string) error
	// RecordDiscordUserMFA saves the 2FA status Discord reported for a user
	// at sign-in, which guilds requiring 2FA check before staff act.
	RecordDiscordUserMFA(ctx context.Context, discordUserID string, enabled bool, checkedAt time.Time) error
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error)
	PingDatabase(ctx context.Context) error
	PingRedis(ctx context.Context) error
	MigrationReadiness(ctx context.Context) (uint64, error)
	OperationalMetricSnapshot(ctx context.Context) (map[string]int64, error)
}

// DiscordStatus reports the bot's gateway connection for /status and /readyz.
type DiscordStatus interface {
	Status() (connected bool, username string, latencyMS int64)
}

// TemplateChangeHandler is told after a template is updated or archived, so
// features that reference templates (the honeypot) can recheck them.
type TemplateChangeHandler interface {
	HandleTemplateChange(ctx context.Context, guildID, templateID string)
}

// Deps are the Server's collaborators. Modules, TemplateChanges, and
// Directory may be nil; everything else is required.
type Deps struct {
	Services *quack.Services
	Store    Storage
	// Redis backs the rate limiter and the idempotency store. Both fail
	// closed: if Redis is down, limited routes return 503.
	Redis   redis.UniversalClient
	Discord DiscordStatus
	// Modules mounts the optional modules' routes.
	Modules         func(mux *ModuleMux)
	TemplateChanges TemplateChangeHandler
	// Directory backs the /directory display routes. Without one they
	// answer 503 dependency_unavailable.
	Directory Directory
}

// Server is the HTTP API. It is an http.Handler; Run serves it on the
// configured port.
type Server struct {
	cfg             config.Config
	services        *quack.Services
	store           Storage
	discord         DiscordStatus
	templateChanges TemplateChangeHandler
	directory       Directory
	limiter         *rateLimiter
	idempotency     *idempotencyStore
	oauth           oauthClient
	trustedProxies  []*net.IPNet

	mux *http.ServeMux
	// table records every route on mux, for the HTTP contract.
	table []Route
	// ownErrorBodies holds, per route pattern, the error statuses whose
	// body the handler writes itself (see Doc.Also); observe leaves those
	// bodies alone instead of turning them into the error envelope.
	ownErrorBodies map[string][]int
	handler        http.Handler
}

// New builds the server and its route table. cfg must already have passed
// config.Validate.
func New(cfg config.Config, deps Deps) (*Server, error) {
	proxies, err := parseTrustedProxies(cfg.API.TrustedProxies)
	if err != nil {
		return nil, err
	}
	s := &Server{
		cfg:             cfg,
		services:        deps.Services,
		store:           deps.Store,
		discord:         deps.Discord,
		templateChanges: deps.TemplateChanges,
		directory:       deps.Directory,
		limiter:         newRateLimiter(deps.Redis),
		idempotency:     newIdempotencyStore(deps.Redis),
		oauth:           defaultOAuthClient(),
		trustedProxies:  proxies,
		mux:             http.NewServeMux(),
	}
	s.routes()
	if deps.Modules != nil {
		deps.Modules(&ModuleMux{s: s})
	}
	s.handler = chain(http.HandlerFunc(s.route),
		requestContext,
		s.observe,
		securityHeaders,
		s.cors,
		s.bodyLimit,
		s.csrf,
	)
	return s, nil
}

// ServeHTTP resolves the route once, so logging and rate-limit subjects can
// use the pattern, then runs the request through the pipeline.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, pattern := s.mux.Handler(r)
	s.handler.ServeHTTP(w, r.WithContext(withRoute(r.Context(), pattern)))
}

// route is the end of the global pipeline. Unknown paths and wrong methods
// are both 404s, as they were before the API moved to net/http.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if routeFrom(r.Context()) == "" {
		writeError(w, r, http.StatusNotFound, codeNotFound, "resource not found")
		return
	}
	s.mux.ServeHTTP(w, r)
}

// Run serves the API until ctx is canceled, then drains in-flight requests
// until shutdownDeadline. The caller supplies the deadline so the HTTP drain
// and the rest of shutdown share one api.shutdown_timeout; Run calls it once
// ctx ends.
func (s *Server) Run(ctx context.Context, shutdownDeadline func() time.Time) error {
	return serve(ctx, newHTTPServer(s.cfg, s), shutdownDeadline)
}

// serve owns the listener and returns only after shutdown has finished, so
// no handler can still be using storage when the caller closes it. If the
// drain times out, remaining connections are closed.
func serve(ctx context.Context, server *http.Server, shutdownDeadline func() time.Time) error {
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen for HTTP: %w", err)
	}
	defer listener.Close()
	slog.InfoContext(ctx, "HTTP API listening", "address", listener.Addr().String())
	serveDone := make(chan struct{})
	shutdownResult := make(chan error, 1)
	go func() {
		select {
		case <-serveDone:
			shutdownResult <- nil
			return
		case <-ctx.Done():
		}
		shutdownCtx, cancel := context.WithDeadline(context.Background(), shutdownDeadline())
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			slog.Error("Failed to gracefully shut down API", "error", err)
			err = errors.Join(err, server.Close())
		}
		shutdownResult <- err
	}()
	serveErr := server.Serve(listener)
	close(serveDone)
	shutdownErr := <-shutdownResult
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	return errors.Join(serveErr, shutdownErr)
}

// newHTTPServer applies the configured port and timeouts.
func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              ":" + cfg.API.Port,
		Handler:           handler,
		ReadHeaderTimeout: cfg.API.ReadHeaderTimeout,
		ReadTimeout:       cfg.API.ReadTimeout,
		WriteTimeout:      cfg.API.WriteTimeout,
		IdleTimeout:       cfg.API.IdleTimeout,
	}
}
