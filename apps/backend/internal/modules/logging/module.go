package logging

import (
	"context"
	"errors"
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

// Module is general logging wired to Discord and the API. The app registers
// its gateway handlers and routes, requests its Intents, and runs it
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
	service := NewService(registry, audit, delivery{bot: bot, guilds: guilds}, nil)
	return &Module{service: service, registry: registry, guilds: guilds, pool: NewPool(service)}
}

// NewPool returns the bounded queue that delivers events through service.
func NewPool(service *Service) *modules.Pool[Event] {
	return modules.NewPool("general_logging", queueCapacity, queueWorkers, func(ctx context.Context, event Event) {
		var err error
		if event.Type == MessageBulkDelete {
			err = service.HandleBulkDelete(ctx, event.GuildID, event.ChannelDiscordID, event.MessageIDs)
		} else {
			err = service.Handle(ctx, event)
		}
		if err != nil && event.Type != MessageBulkDelete {
			// Message content is never logged.
			slog.ErrorContext(ctx, "General logging delivery failed", "guild_id", event.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
	})
}

// MountHTTP mounts the general logging routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, modules.RequestActor)
}

// Start starts the delivery workers.
func (m *Module) Start(ctx context.Context) { m.pool.Start(ctx) }

// Stop drains queued deliveries, giving up when ctx is done.
func (m *Module) Stop(ctx context.Context) error { return m.pool.Stop(ctx) }

// Intents returns the gateway intents logging needs once any guild has it
// on: members, moderation, messages, and message content.
func (m *Module) Intents(ctx context.Context) (discordgo.Intent, error) {
	enabled, err := m.registry.AnyEnabled(ctx, modules.GeneralLogging)
	if err != nil || !enabled {
		return 0, err
	}
	return discordgo.IntentGuilds | discordgo.IntentGuildMembers | discordgo.IntentGuildModeration |
		discordgo.IntentGuildMessages | discordgo.IntentMessageContent, nil
}

// delivery is the DeliveryClient that posts to Discord.
type delivery struct {
	bot    *discord.Bot
	guilds *modules.Guilds
}

// SendStaffLog posts payload with mentions suppressed, after re-checking
// that the channel is still staff-only.
func (d delivery) SendStaffLog(ctx context.Context, guildID, channelID, payload string) error {
	if err := d.ValidateStaffOnlyChannel(ctx, guildID, channelID); err != nil {
		return err
	}
	_, err := d.bot.Session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: payload, AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	return err
}

// ValidateStaffOnlyChannel applies discord.Bot.ValidateStaffChannel to the
// guild with internal ID guildID, then checks that the bot can post there,
// so a bad destination fails when it is saved rather than on every event.
func (d delivery) ValidateStaffOnlyChannel(ctx context.Context, guildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	discordGuildID, err := d.guilds.DiscordID(ctx, guildID)
	if err != nil {
		return err
	}
	if err := d.bot.ValidateStaffChannel(ctx, discordGuildID, channelID); err != nil {
		return err
	}
	session := d.bot.Session
	if session.State == nil || session.State.User == nil {
		return errors.New("discord bot identity is unavailable")
	}
	permissions, err := session.UserChannelPermissions(session.State.User.ID, channelID)
	if err != nil {
		return err
	}
	if permissions&discordgo.PermissionViewChannel == 0 || permissions&discordgo.PermissionSendMessages == 0 {
		return errors.New("discord bot cannot deliver to logging destination")
	}
	return nil
}
