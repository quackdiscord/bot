// Package app is Quack's composition root. It builds every component from
// the config, wires them together, runs them, and shuts them down in
// reverse order.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/api"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/logging"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/worker"
	"github.com/redis/go-redis/v9"
)

// Background loop intervals.
const (
	appealInterval      = 5 * time.Second
	appealBatch         = 50
	auditMirrorInterval = 5 * time.Second
	transcriptInterval  = time.Hour
)

// Run starts Quack and blocks until ctx is canceled or a component fails,
// then stops everything it started, newest first, within
// api.shutdown_timeout. cfg must already be validated and the default
// logger set.
func Run(ctx context.Context, cfg config.Config) (err error) {
	slog.InfoContext(ctx, "Starting Quack", "environment", cfg.Environment)
	var stops stopList
	defer func() {
		slog.Info("Stopping Quack")
		err = errors.Join(err, stops.run(cfg.API.ShutdownTimeout))
		if err == nil {
			slog.Info("Quack stopped cleanly")
		}
	}()

	db, err := store.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	stops.add("database", func(context.Context) error { return sqlDB.Close() })
	rdb, err := store.OpenRedis(cfg.Redis.URL)
	if err != nil {
		return err
	}
	stops.add("redis", func(context.Context) error { return rdb.Close() })
	st := store.New(db, rdb)
	if err := st.Migrate(); err != nil {
		return fmt.Errorf("migrate storage: %w", err)
	}
	slog.InfoContext(ctx, "Storage ready")

	bot, err := discord.New(cfg.Discord.Token)
	if err != nil {
		return fmt.Errorf("create Discord bot: %w", err)
	}
	a, err := build(ctx, cfg, st, rdb, bot)
	if err != nil {
		return err
	}
	if err := discord.SyncCommands(ctx, bot, rdb, discord.SyncOptions{
		AppID:   cfg.Discord.AppID,
		GuildID: cfg.Discord.CommandGuildID,
		Prune:   cfg.Discord.CommandPrune,
	}); err != nil {
		return fmt.Errorf("sync Discord commands: %w", err)
	}

	a.worker.Start(ctx, a.services.Actions.ProcessCaseActions, st)
	stops.add("worker", a.worker.Stop)
	slog.InfoContext(ctx, "Action workers started", "workers", cfg.Queue.Workers, "capacity", cfg.Queue.Size)
	a.logging.Start(ctx)
	stops.add("general logging", a.logging.Stop)
	a.honeypot.Start(ctx)
	stops.add("honeypot", a.honeypot.Stop)

	if err := bot.Open(); err != nil {
		return fmt.Errorf("connect Discord bot: %w", err)
	}
	stops.add("discord", func(ctx context.Context) error { return closeBot(ctx, bot) })
	return a.server.Run(ctx)
}

// quackApp is everything build wires together, ready to start.
type quackApp struct {
	services *quack.Services
	worker   *worker.Worker
	tickets  *tickets.Module
	logging  *logging.Module
	honeypot *honeypot.Module
	server   *api.Server
}

// build wires the domain, the optional modules, the background loops, the
// Discord handlers, and the API around st, rdb, and bot. It starts nothing
// and touches the network only through st and rdb, so tests can call it.
func build(ctx context.Context, cfg config.Config, st *store.Store, rdb *redis.Client, bot *discord.Bot) (*quackApp, error) {
	w := worker.New(cfg.Queue.Size, cfg.Queue.Workers)
	registry := modules.NewRegistry(st.DB())
	services := quack.New(quack.Deps{
		Store:            st,
		Guilds:           bot,
		Enforcer:         bot,
		Messenger:        bot,
		Evidence:         bot,
		Channels:         bot,
		Scheduler:        w,
		Modules:          registry,
		DashboardBaseURL: quack.DashboardBaseURL(cfg.API.CORSOrigins),
	})

	audit := modules.NewAuditLog(st)
	guilds := modules.NewGuilds(st)
	a := &quackApp{
		services: services,
		worker:   w,
		tickets:  tickets.New(st.DB(), registry, audit, guilds, bot.Session, services.Guilds),
		logging:  logging.New(registry, audit, guilds, bot),
		honeypot: honeypot.New(st.DB(), registry, audit, guilds, bot.Session, st, services.Cases),
	}

	appeals := quack.NewAppealNotificationDispatcher(st, discord.NewAppealNotifier(bot, st))
	w.Every("appeal notifications", appealInterval, func(ctx context.Context) error {
		return appeals.DispatchPending(ctx, appealBatch)
	})
	w.Every("audit mirror", auditMirrorInterval, quack.NewAuditMirror(st, bot).PollOnce)
	w.Every("ticket transcripts", transcriptInterval, a.tickets.SweepTranscripts)

	// Request only the gateway intents that enabled modules need. A module
	// switched on later gets its events after the next restart.
	intents := discordgo.IntentGuilds
	for _, m := range []interface {
		Intents(context.Context) (discordgo.Intent, error)
	}{a.logging, a.honeypot} {
		extra, err := m.Intents(ctx)
		if err != nil {
			return nil, fmt.Errorf("derive gateway intents: %w", err)
		}
		intents |= extra
	}
	bot.Session.Identify.Intents = intents

	discord.HandleGuildLifecycle(bot, services)
	a.tickets.RegisterGateway(bot.Session)
	a.logging.RegisterGateway(bot.Session)
	a.honeypot.RegisterGateway(bot.Session)
	router := discord.NewRouter(bot, services, discord.NewRedisDeduper(rdb))
	a.tickets.RegisterComponents(router)

	server, err := api.New(cfg, api.Deps{
		Services: services,
		Store:    st,
		Redis:    rdb,
		Discord:  bot,
		Modules: func(mux *api.ModuleMux) {
			a.tickets.MountHTTP(mux)
			a.logging.MountHTTP(mux)
			a.honeypot.MountHTTP(mux)
		},
		TemplateChanges: a.honeypot,
	})
	if err != nil {
		return nil, fmt.Errorf("build HTTP API: %w", err)
	}
	a.server = server
	return a, nil
}

// closeBot closes the gateway, giving up when ctx ends in case the
// websocket library stalls.
func closeBot(ctx context.Context, bot *discord.Bot) error {
	done := make(chan error, 1)
	go func() { done <- bot.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// stopList is the shutdown sequence: each started component adds its stop
// func, and run calls them newest first.
type stopList []stopper

// stopper is one named stop func.
type stopper struct {
	name string
	stop func(context.Context) error
}

func (s *stopList) add(name string, stop func(context.Context) error) {
	*s = append(*s, stopper{name, stop})
}

// run stops everything, sharing one timeout, and returns every failure.
func (s stopList) run(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var errs []error
	for i := len(s) - 1; i >= 0; i-- {
		if err := s[i].stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", s[i].name, err))
		}
	}
	return errors.Join(errs...)
}
