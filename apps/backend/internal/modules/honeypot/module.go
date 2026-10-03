package honeypot

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/gorm"
)

// Queue bounds for trap messages. They are separate from the case queue so
// a flood of messages cannot delay moderation.
const (
	queueCapacity = 256
	queueWorkers  = 2
)

// Module is the honeypot wired to Discord and the API. The app mounts its
// routes, registers its gateway handlers, requests its Intents, tells it
// about template changes, and runs it between Start and Stop.
type Module struct {
	service   *Service
	registry  *modules.Registry
	guilds    *modules.Guilds
	templates templateValidator
	pool      *modules.Pool[Message]
}

// New returns the honeypot module. Triggers are stored in db, settings in
// registry, events audited to audit; session checks the trap channel,
// templateStore is the template source, and cases opens the resulting cases.
func New(db *gorm.DB, registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, session *discordgo.Session, templateStore TemplateStore, cases CaseCreator) *Module {
	templates := templateValidator{store: templateStore}
	channels := channelValidator{session: session, guilds: guilds}
	service := NewService(registry, NewStore(db), audit, channels, templates, caseApplier{cases: cases})
	return &Module{
		service:   service,
		registry:  registry,
		guilds:    guilds,
		templates: templates,
		pool:      NewPool(service),
	}
}

// NewPool returns the bounded queue that runs trap messages through
// service.
func NewPool(service *Service) *modules.Pool[Message] {
	return modules.NewPool("honeypot", queueCapacity, queueWorkers, func(ctx context.Context, message Message) {
		if _, err := service.HandleMessage(ctx, message); err != nil {
			slog.ErrorContext(ctx, "Honeypot event failed",
				"guild_id", message.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
	})
}

// MountHTTP mounts the honeypot routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, modules.RequestActor)
}

// RegisterGateway subscribes the honeypot to new messages and to deletions
// of its trap channel or guild.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onChannelDelete)
	session.AddHandler(m.onGuildDelete)
}

// Intents returns the gateway intents the honeypot needs: guild messages,
// once any guild has it on. It never needs message content.
func (m *Module) Intents(ctx context.Context) (discordgo.Intent, error) {
	enabled, err := m.registry.AnyEnabled(ctx, modules.Honeypots)
	if err != nil || !enabled {
		return 0, err
	}
	return discordgo.IntentGuilds | discordgo.IntentGuildMessages, nil
}

// Start starts the trap message workers.
func (m *Module) Start(ctx context.Context) { m.pool.Start(ctx) }

// Stop drains queued trap messages, giving up when ctx is done.
func (m *Module) Stop(ctx context.Context) error { return m.pool.Stop(ctx) }

// HandleTemplateChange turns the honeypot off if a template edit or archive
// left its template unusable unattended. It implements
// api.TemplateChangeHandler.
func (m *Module) HandleTemplateChange(ctx context.Context, guildID, templateID string) {
	if m.templates.ValidateHoneypotTemplate(ctx, guildID, templateID) != nil {
		_ = m.service.HandleTemplateUnavailable(ctx, guildID, templateID)
	}
}
