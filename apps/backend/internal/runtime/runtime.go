package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/quackdiscord/bot/internal/api"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/workqueue"
)

// Run assembles every adapter around the application core, starts the
// process, and shuts dependencies down in reverse order. cfg must already be
// validated, and the default logger set.
func Run(ctx context.Context, cfg config.Config) (runErr error) {
	slog.InfoContext(ctx, "Starting Quack", "environment", cfg.Environment)
	db, err := store.OpenMySQL(cfg.Database.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	redis, err := store.OpenRedis(cfg.Redis.URL)
	if err != nil {
		return err
	}
	defer redis.Close()

	repositories := store.New(db, redis)
	if err := repositories.Migrate(); err != nil {
		return fmt.Errorf("migrate storage: %w", err)
	}
	slog.InfoContext(ctx, "Storage ready")

	bot, err := discord.New(cfg.Discord.Token)
	if err != nil {
		return fmt.Errorf("create Discord bot: %w", err)
	}
	queue := workqueue.New(cfg.Queue.Size, cfg.Queue.Workers)
	var moduleRuntime *moduleintegration.Runtime
	queueStarted := false
	defer func() {
		slog.Info("Stopping Quack")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.API.ShutdownTimeout)
		defer cancel()
		var shutdownErrors []error
		if queueStarted {
			shutdownErrors = append(shutdownErrors, queue.StopContext(shutdownCtx))
		}
		if moduleRuntime != nil {
			shutdownErrors = append(shutdownErrors, moduleRuntime.CloseContext(shutdownCtx))
		}
		shutdownErrors = append(shutdownErrors, closeDiscord(shutdownCtx, bot))
		runErr = errors.Join(runErr, errors.Join(shutdownErrors...))
		if runErr == nil {
			slog.Info("Quack stopped cleanly")
		}
	}()
	services := quack.New(quack.Deps{
		Store:            repositories,
		Guilds:           bot,
		Enforcer:         bot,
		Messenger:        bot,
		Evidence:         bot,
		Channels:         bot,
		Scheduler:        queue,
		DashboardBaseURL: quack.DashboardBaseURL(cfg.API.CORSOrigins),
	})
	moduleRuntime, err = moduleintegration.New(ctx, repositories, bot, services)
	if err != nil {
		return fmt.Errorf("compose optional modules: %w", err)
	}
	intents, err := moduleRuntime.RequiredGatewayIntents(ctx)
	if err != nil {
		return fmt.Errorf("derive optional module gateway intents: %w", err)
	}
	bot.Session.Identify.Intents = intents
	discord.HandleGuildLifecycle(bot, services)
	if err := moduleRuntime.RegisterGatewayHandlers(bot.Session); err != nil {
		return fmt.Errorf("register optional module gateway handlers: %w", err)
	}
	router := discord.NewRouter(bot, services, discord.NewRedisDeduper(redis))
	if err := moduleRuntime.RegisterComponents(router); err != nil {
		return fmt.Errorf("register Discord components: %w", err)
	}
	syncOptions := discord.SyncOptions{
		AppID:   cfg.Discord.AppID,
		GuildID: cfg.Discord.CommandGuildID,
		Prune:   cfg.Discord.CommandPrune,
	}
	if err := discord.SyncCommands(ctx, bot, redis, syncOptions); err != nil {
		return fmt.Errorf("sync Discord commands: %w", err)
	}
	if err := bot.Open(); err != nil {
		return fmt.Errorf("connect Discord bot: %w", err)
	}

	queue.Start(ctx, services.Actions.ProcessCaseActions, repositories)
	queueStarted = true
	slog.InfoContext(ctx, "Action workers started", "workers", cfg.Queue.Workers, "capacity", cfg.Queue.Size)

	server, err := api.New(cfg, api.Deps{
		Services:        services,
		Store:           repositories,
		Redis:           redis,
		Discord:         bot,
		Modules:         moduleRuntime,
		TemplateChanges: moduleRuntime,
	})
	if err != nil {
		return fmt.Errorf("build HTTP API: %w", err)
	}
	return server.Run(ctx)
}

// closeDiscord bounds adapter close even if an upstream websocket library stalls.
func closeDiscord(ctx context.Context, bot *discord.Bot) error {
	done := make(chan error, 1)
	go func() { done <- bot.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
