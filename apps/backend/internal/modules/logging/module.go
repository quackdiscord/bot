package logging

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// Delivery queue bounds. Logging is the noisiest module, so it sheds events
// under load rather than delay the gateway or moderation.
const (
	queueCapacity = 1000
	queueWorkers  = 2
)

// defaultCacheLimit is how many messages a guild caches until its settings
// are loaded.
const defaultCacheLimit = 1000

// Module is general logging wired to Discord and the API. The app mounts its
// routes, registers its gateway handlers, requests its Intents, and runs it
// between Start and Stop.
type Module struct {
	service  *Service
	registry *modules.Registry
	guilds   *modules.Guilds
	pool     *modules.Pool[Event]
}

// New returns the logging module. Settings live in registry and changes are
// audited to audit; bot delivers to channels that pass its staff-only check.
func New(registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, bot *discord.Bot) *Module {
	client := delivery{bot: bot, guilds: guilds}
	service := NewService(registry, audit, client, NewMessageCache(defaultCacheLimit))
	return &Module{service: service, registry: registry, guilds: guilds, pool: NewPool(service)}
}

// NewPool returns the bounded queue that delivers events through service.
func NewPool(service *Service) *modules.Pool[Event] {
	return modules.NewPool("general_logging", queueCapacity, queueWorkers, func(ctx context.Context, event Event) {
		if event.Type == MessageBulkDelete {
			_ = service.HandleBulkDelete(ctx, event.GuildID, event.ChannelDiscordID, event.MessageIDs)
			return
		}
		if err := service.Handle(ctx, event); err != nil {
			// Only the error's type is logged: its text may quote message
			// content.
			slog.ErrorContext(ctx, "General logging delivery failed",
				"guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
	})
}

// MountHTTP mounts the general logging routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, modules.RequestActor)
}

// RegisterGateway subscribes logging to the gateway events it logs, and to
// channel deletions so routes to a deleted channel are removed.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onMessageUpdate)
	session.AddHandler(m.onMessageDelete)
	session.AddHandler(m.onMessageDeleteBulk)
	session.AddHandler(m.onMemberAdd)
	session.AddHandler(m.onMemberRemove)
	session.AddHandler(m.onBanAdd)
	session.AddHandler(m.onBanRemove)
	session.AddHandler(m.onGuildUpdate)
	session.AddHandler(m.onChannelCreate)
	session.AddHandler(m.onChannelUpdate)
	session.AddHandler(m.onChannelDelete)
}

// Intents returns the gateway intents logging needs once any guild has it
// on: members, moderation, messages, and message content.
func (m *Module) Intents(ctx context.Context) (discordgo.Intent, error) {
	enabled, err := m.registry.AnyEnabled(ctx, modules.GeneralLogging)
	if err != nil || !enabled {
		return 0, err
	}
	return discordgo.IntentGuilds |
		discordgo.IntentGuildMembers |
		discordgo.IntentGuildModeration |
		discordgo.IntentGuildMessages |
		discordgo.IntentMessageContent, nil
}

// Start starts the delivery workers.
func (m *Module) Start(ctx context.Context) { m.pool.Start(ctx) }

// Stop drains queued deliveries, giving up when ctx is done.
func (m *Module) Stop(ctx context.Context) error { return m.pool.Stop(ctx) }
