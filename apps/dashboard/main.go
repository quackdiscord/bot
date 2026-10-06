// Command quack-dashboard serves the Quack web dashboard.
//
// It serves the built single-page app (embedded at compile time, or read
// from -dir) and reverse-proxies /api/* to the Quack API, so the browser
// talks to one origin. Build the app with `bun run build` before compiling.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// dist holds the production build. Vite writes it to dist/app; dist/.gitkeep
// keeps the directory present so the package compiles before a build.
//
//go:embed all:dist
var dist embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("quack-dashboard stopped", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("quack-dashboard", flag.ContinueOnError)
	addr := flags.String("addr", ":"+env("QUACK_DASHBOARD_PORT", "3000"), "listen address")
	apiURL := flags.String("api", env("QUACK_DASHBOARD_API_URL", "http://localhost:8080"), "Quack API base URL")
	dir := flags.String("dir", os.Getenv("QUACK_DASHBOARD_DIR"), "serve the app from this directory instead of the embedded build")
	if err := flags.Parse(args); err != nil {
		return err
	}

	api, err := url.Parse(*apiURL)
	if err != nil || (api.Scheme != "http" && api.Scheme != "https") || api.Host == "" {
		return fmt.Errorf("invalid API URL %q: want http(s)://host[:port]", *apiURL)
	}
	site, err := siteFS(*dir)
	if err != nil {
		return err
	}
	handler, err := newServer(site, api)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	slog.Info("quack-dashboard listening", "address", *addr, "api", api.String())

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// siteFS returns the directory to serve: dir when set, otherwise the
// embedded build.
func siteFS(dir string) (fs.FS, error) {
	if dir != "" {
		return os.DirFS(dir), nil
	}
	return fs.Sub(dist, "dist/app")
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
