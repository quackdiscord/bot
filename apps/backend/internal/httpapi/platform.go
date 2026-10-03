package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
)

// PlatformRegistrar validates and installs the shared HTTP security and contract middleware for integration-owned routers.
type PlatformRegistrar struct {
	cfg        config.Config
	primitives *httpplatform.Primitives
}

// NewPlatformRegistrarWithRepository constructs the production platform with
// Redis-backed endpoint rate and idempotency policy enabled.
func NewPlatformRegistrarWithRepository(cfg config.Config, repository any) (*PlatformRegistrar, error) {
	registrar, err := NewPlatformRegistrar(cfg)
	if err != nil {
		return nil, err
	}
	primitives := httpplatform.FromRepository(repository)
	registrar.primitives = &primitives
	return registrar, nil
}

// NewPlatformRegistrar constructs the reusable QP-B platform registrar.
func NewPlatformRegistrar(cfg config.Config) (*PlatformRegistrar, error) {
	if err := middleware.ValidateSecurityConfig(cfg); err != nil {
		return nil, err
	}
	return &PlatformRegistrar{cfg: cfg}, nil
}

// Register installs trusted-proxy handling and middleware in the required order before feature routes are registered.
func (p *PlatformRegistrar) Register(r *gin.Engine) error {
	if err := r.SetTrustedProxies(p.cfg.API.TrustedProxies); err != nil {
		return err
	}
	r.Use(middleware.RequestContext)
	r.Use(middleware.ErrorEnvelope)
	r.Use(middleware.Logger)
	r.Use(middleware.Recovery)
	r.Use(middleware.SecurityHeaders)
	r.Use(middleware.CORS(p.cfg.API.CORSOrigins))
	r.Use(middleware.BodyLimit(p.cfg.API.MaxBodyBytes))
	r.Use(middleware.CSRF(p.cfg.Auth, p.cfg.API.CORSOrigins))
	if p.primitives != nil {
		r.Use(httpplatform.EndpointPolicy(*p.primitives, p.cfg))
	}
	return nil
}
