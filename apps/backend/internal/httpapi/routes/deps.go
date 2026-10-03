package routes

import (
	"context"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// Deps is what the HTTP routes need: the domain services, the process
// configuration, and the storage that the auth, health, and ops handlers
// use directly.
type Deps struct {
	*quack.Services
	Config config.Config
	Store  Storage
}

// Storage is the storage the routes use directly, outside the domain
// services. Handlers may also type-assert it for optional capabilities such
// as migration readiness and metrics.
type Storage interface {
	middleware.SessionStore
	SaveOAuthState(ctx context.Context, stateID string, state *quack.OAuthState, ttl time.Duration) error
	ConsumeOAuthState(ctx context.Context, stateID string) (*quack.OAuthState, error)
	SaveSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) error
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error)
	PingDatabase(ctx context.Context) error
	PingRedis(ctx context.Context) error
}
