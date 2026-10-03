package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/routes"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
)

// Run serves the configured HTTP API until the context is canceled, then performs a bounded graceful shutdown.
func Run(ctx context.Context, cfg config.Config, services *quack.Services, moduleRuntime *moduleintegration.Runtime, discord routes.DiscordStatusProvider) error {
	if cfg.Environment != "dev" {
		gin.SetMode(gin.ReleaseMode)
	}

	registrar, err := NewPlatformRegistrarWithRepository(cfg, services.Store)
	if err != nil {
		return fmt.Errorf("validate HTTP platform configuration: %w", err)
	}

	r := gin.New()
	if err := registrar.Register(r); err != nil {
		return fmt.Errorf("configure trusted HTTP proxies: %w", err)
	}

	if err := routes.SetupRoutesWithModules(r, services, moduleRuntime, discord); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}

	server := newHTTPServer(cfg, r)
	return serve(ctx, server, cfg.API.ShutdownTimeout)
}

// serve owns the listener and joins shutdown before dependencies are closed.
// A bind failure creates no shutdown goroutine; a drain timeout force-closes
// connections so handlers cannot keep using storage after Run returns.
func serve(ctx context.Context, server *http.Server, shutdownTimeout time.Duration) error {
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
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
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

// newHTTPServer constructs the bounded standard-library server used by Run.
func newHTTPServer(cfg config.Config, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.API.Port),
		Handler:           handler,
		ReadHeaderTimeout: cfg.API.ReadHeaderTimeout,
		ReadTimeout:       cfg.API.ReadTimeout,
		WriteTimeout:      cfg.API.WriteTimeout,
		IdleTimeout:       cfg.API.IdleTimeout,
	}
}
