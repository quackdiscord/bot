package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/discordbot/commands"
	"github.com/quackdiscord/bot/internal/httpapi"
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
	if _, err := httpapi.NewPlatformRegistrar(cfg); err != nil {
		return fmt.Errorf("validate HTTP security configuration: %w", err)
	}
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

	bot, err := discordbot.New(cfg.Discord.Token)
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
	services := quack.NewWithConfigDependencies(cfg, repositories, bot, bot, queue)
	moduleRuntime, err = moduleintegration.New(ctx, repositories, bot.Session, services, bot)
	if err != nil {
		return fmt.Errorf("compose optional modules: %w", err)
	}
	intents, err := moduleRuntime.RequiredGatewayIntents(ctx)
	if err != nil {
		return fmt.Errorf("derive optional module gateway intents: %w", err)
	}
	bot.Session.Identify.Intents = intents
	if err := discordbot.RegisterGuildLifecycle(bot.Session, services); err != nil {
		return fmt.Errorf("register Discord guild lifecycle: %w", err)
	}
	if err := moduleRuntime.RegisterGatewayHandlers(bot.Session); err != nil {
		return fmt.Errorf("register optional module gateway handlers: %w", err)
	}
	if err := commands.Register(bot.Session, services, moduleRuntime.RegisterComponents); err != nil {
		return fmt.Errorf("register Discord commands and components: %w", err)
	}
	if err := bot.Open(); err != nil {
		return fmt.Errorf("connect Discord bot: %w", err)
	}

	queue.Start(ctx, services.Actions.ProcessCaseActions, repositories)
	queueStarted = true
	slog.InfoContext(ctx, "Action workers started", "workers", cfg.Queue.Workers, "capacity", cfg.Queue.Size)

	return httpapi.Run(ctx, cfg, services, moduleRuntime, bot)
}

// closeDiscord bounds adapter close even if an upstream websocket library stalls.
func closeDiscord(ctx context.Context, bot *discordbot.Bot) error {
	done := make(chan error, 1)
	go func() { done <- bot.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
