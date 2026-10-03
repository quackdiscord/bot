// Package app is Quack's composition root. It builds every component from
// the config, wires them together, runs them, and shuts them down in
// reverse order.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
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

// Background loop intervals and batch sizes.
const (
	appealInterval      = 5 * time.Second
	appealBatch         = 50
	auditMirrorInterval = 5 * time.Second
	transcriptInterval  = time.Hour
	honeypotInterval    = time.Second
)

// Run starts Quack and blocks until ctx is canceled or a component fails,
// then stops everything it started, newest first. The HTTP drain and every
// stop after it share one api.shutdown_timeout, which starts when shutdown
// begins. cfg must already be validated and the default logger set.
func Run(ctx context.Context, cfg config.Config) (err error) {
	slog.InfoContext(ctx, "Starting Quack", "environment", cfg.Environment)
	// The deadline is fixed by whichever comes first: the HTTP server seeing
	// ctx end, or Run returning on its own.
	shutdownDeadline := sync.OnceValue(func() time.Time {
		return time.Now().Add(cfg.API.ShutdownTimeout)
	})
	var stops stopList
	defer func() {
		slog.Info("Stopping Quack")
		err = errors.Join(err, stops.run(shutdownDeadline()))
		if err == nil {
			slog.Info("Quack stopped cleanly")
		}
	}()

	st, rdb, err := openStorage(ctx, cfg, &stops)
	if err != nil {
		return err
	}
	bot, err := discord.New(cfg.Discord.Token)
	if err != nil {
		return fmt.Errorf("create Discord bot: %w", err)
	}
	q, err := build(ctx, cfg, st, rdb, bot)
	if err != nil {
		return err
	}
	if err := discord.SyncCommands(ctx, bot, rdb, discord.SyncOptions{
		AppID:   cfg.Discord.AppID,
		GuildID: cfg.Discord.CommandGuildID,
		Prune:   cfg.Discord.CommandPrune,
		Dev:     cfg.Environment == "dev",
	}); err != nil {
		return fmt.Errorf("sync Discord commands: %w", err)
	}
	// Background work starts before the gateway opens, so the first events
	// and interactions already have somewhere to go.
	q.startBackground(ctx, st, &stops)
	if err := bot.Open(); err != nil {
		return fmt.Errorf("connect Discord bot: %w", err)
	}
	stops.add("discord", func(ctx context.Context) error { return closeBot(ctx, bot) })
	return q.server.Run(ctx, shutdownDeadline)
}

// openStorage connects to MySQL and Redis and migrates the schema. Both
// connections are added to stops as soon as they open.
func openStorage(ctx context.Context, cfg config.Config, stops *stopList) (*store.Store, *redis.Client, error) {
	db, err := store.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		return nil, nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, nil, err
	}
	stops.add("database", func(context.Context) error { return sqlDB.Close() })
	rdb, err := store.OpenRedis(cfg.Redis.URL)
	if err != nil {
		return nil, nil, err
	}
	stops.add("redis", func(context.Context) error { return rdb.Close() })
	st := store.New(db, rdb)
	if err := st.Migrate(); err != nil {
		return nil, nil, fmt.Errorf("migrate storage: %w", err)
	}
	slog.InfoContext(ctx, "Storage ready")
	return st, rdb, nil
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
	bot.DashboardURL = quack.DashboardBaseURL(cfg.API.CORSOrigins)
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
	q := &quackApp{
		services: services,
		worker:   w,
		tickets:  tickets.New(st.DB(), registry, audit, guilds, bot.Session, services.Guilds),
		logging:  logging.New(registry, audit, guilds, bot),
		honeypot: honeypot.New(st.DB(), registry, audit, guilds, bot.Session, st, services.Cases, services.Templates),
	}

	appeals := quack.NewAppealNotificationDispatcher(st, discord.NewAppealNotifier(bot, st))
	w.Every("appeal notifications", appealInterval, func(ctx context.Context) error {
		return appeals.DispatchPending(ctx, appealBatch)
	})
	w.Every("audit mirror", auditMirrorInterval, quack.NewAuditMirror(st, bot).PollOnce)
	w.Every("ticket transcripts", transcriptInterval, q.tickets.SweepTranscripts)
	w.Every("case publications", discord.PublicationRefreshInterval,
		discord.NewPublicationRefresher(bot, services.Publications).RefreshDue)
	w.Every("honeypot upkeep", honeypotInterval, q.honeypot.Sweep)
	w.Every("honeypot warnings", honeypotInterval, q.honeypot.RefreshWarnings)

	bot.Session.Identify.Intents = gatewayIntents(q.logging, q.honeypot, q.tickets)

	discord.HandleGuildLifecycle(bot, services)
	q.tickets.RegisterGateway(bot.Session)
	q.logging.RegisterGateway(bot.Session)
	q.honeypot.RegisterGateway(bot.Session)
	router := discord.NewRouter(bot, services, discord.NewRedisDeduper(rdb))
	q.tickets.RegisterComponents(router)
	q.logging.RegisterSetup(router)
	q.honeypot.RegisterSetup(router)

	server, err := api.New(cfg, api.Deps{
		Services:        services,
		Store:           st,
		Redis:           rdb,
		Discord:         bot,
		Modules:         mountModules(q.tickets, q.logging, q.honeypot),
		TemplateChanges: q.honeypot,
	})
	if err != nil {
		return nil, fmt.Errorf("build HTTP API: %w", err)
	}
	q.server = server
	return q, nil
}

// mountModules mounts every optional module's HTTP routes. build and Routes
// share it, so the HTTP contract lists exactly the modules the server
// serves.
func mountModules(t *tickets.Module, l *logging.Module, h *honeypot.Module) func(*api.ModuleMux) {
	return func(mux *api.ModuleMux) {
		t.MountHTTP(mux)
		l.MountHTTP(mux)
		h.MountHTTP(mux)
	}
}

// Routes returns every route the HTTP API mounts, core and module, for
// generating the HTTP contract. It builds the API around empty services and
// modules, so nothing it returns may be served.
func Routes() ([]api.Route, error) {
	server, err := api.New(config.Default(), api.Deps{
		Services: &quack.Services{},
		Modules:  mountModules(&tickets.Module{}, &logging.Module{}, &honeypot.Module{}),
	})
	if err != nil {
		return nil, err
	}
	return server.Routes(), nil
}

// startBackground starts the case action worker and its loops, then the
// module pools, adding each to stops.
func (q *quackApp) startBackground(ctx context.Context, st *store.Store, stops *stopList) {
	q.worker.Start(ctx, q.services.Actions.ProcessCaseActions, st)
	stops.add("worker", q.worker.Stop)
	stats := q.worker.Stats()
	slog.InfoContext(ctx, "Action workers started", "workers", stats.Workers, "capacity", stats.BufferSize)
	q.logging.Start(ctx)
	stops.add("general logging", q.logging.Stop)
	q.honeypot.Start(ctx)
	stops.add("honeypot", q.honeypot.Stop)
}

// intentSource is a module that asks for gateway intents.
type intentSource interface {
	Intents() discordgo.Intent
}

// gatewayIntents returns the gateway intents Quack needs: guilds, plus
// everything the modules can use. Intents are fixed when the gateway
// connects, so they are requested even for modules no guild has on yet;
// otherwise a module switched on with /setup would hear nothing until a
// restart. Members and message content are privileged and must be enabled
// for the application in the Discord developer portal.
func gatewayIntents(sources ...intentSource) discordgo.Intent {
	intents := discordgo.IntentGuilds
	for _, source := range sources {
		intents |= source.Intents()
	}
	return intents
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

// run stops everything before one shared deadline and returns every
// failure.
func (s stopList) run(deadline time.Time) error {
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var errs []error
	for _, stopper := range slices.Backward(s) {
		if err := stopper.stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("stop %s: %w", stopper.name, err))
		}
	}
	return errors.Join(errs...)
}
