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

// Module is the honeypot wired to Discord and the API. The app registers its
// gateway handlers and routes, requests its Intents, tells it about template
// changes, and runs it between Start and Stop.
type Module struct {
	service   *Service
	registry  *modules.Registry
	guilds    *modules.Guilds
	templates templates
	pool      *modules.Pool[Message]
}

// New returns the honeypot module. Triggers are stored in db, settings in
// registry, events audited to audit; session checks the trap channel,
// templates is the template source, and cases opens the resulting cases.
func New(db *gorm.DB, registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, session *discordgo.Session, templateStore TemplateStore, cases CaseCreator) *Module {
	validator := templates{store: templateStore}
	service := NewService(registry, NewStore(db), audit,
		channelValidator{session: session, guilds: guilds}, validator, caseApplier{cases: cases})
	return &Module{
		service:   service,
		registry:  registry,
		guilds:    guilds,
		templates: validator,
		pool:      NewPool(service),
	}
}

// NewPool returns the bounded queue that runs trap messages through
// service.
func NewPool(service *Service) *modules.Pool[Message] {
	return modules.NewPool("honeypot", queueCapacity, queueWorkers, func(ctx context.Context, message Message) {
		if _, err := service.HandleMessage(ctx, message); err != nil {
			slog.ErrorContext(ctx, "Honeypot event failed", "guild_id", message.GuildID, "error_type", fmt.Sprintf("%T", err))
		}
	})
}

// MountHTTP mounts the honeypot routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, modules.RequestActor)
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
