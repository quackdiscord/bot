package tickets

import (
	"context"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/gorm"
)

// StaffResolver resolves a Discord member's live staff context.
// *quack.GuildService implements it.
type StaffResolver interface {
	ResolveDiscordStaffContext(ctx context.Context, input quack.DiscordStaffContextInput) (*quack.GuildStaffContext, error)
}

// Module is the tickets module wired to Discord and the API. The app
// mounts its routes, registers its gateway handlers and components, and runs
// SweepTranscripts periodically.
type Module struct {
	service  *Service
	store    *Store
	discord  *DiscordAdapter
	channels channels
	guilds   *modules.Guilds
	staff    StaffResolver

	// repairMu guards the thread-repair queue; see repairThreads.
	repairMu      sync.Mutex
	repairPending map[string]struct{}
	repairRunning bool
}

// New returns the tickets module. Tickets are stored in db, switched on in
// registry, audited to audit, and created through session; staff resolves
// live authority for button presses.
func New(db *gorm.DB, registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, session *discordgo.Session, staff StaffResolver) *Module {
	store := NewStore(db)
	service := NewService(registry, store, audit)
	channels := channels{session: session, guilds: guilds}
	return &Module{
		service:       service,
		store:         store,
		discord:       NewDiscordAdapter(service, channels),
		channels:      channels,
		guilds:        guilds,
		staff:         staff,
		repairPending: make(map[string]struct{}),
	}
}

// MountHTTP mounts the ticket routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, m.discord, modules.RequestActor)
}

// RegisterGateway subscribes tickets to the gateway events that can change
// who may see a ticket, or delete one.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onGuildCreate)
	session.AddHandler(m.onMemberUpdate)
	session.AddHandler(m.onRoleUpdate)
	session.AddHandler(m.onRoleDelete)
	session.AddHandler(m.onChannelDelete)
}

// RegisterComponents installs the ticket buttons and the reply modal on
// router.
func (m *Module) RegisterComponents(router *discord.Router) {
	router.HandleComponent(componentNamespace, "open", m.openComponent)
	router.HandleComponent(componentNamespace, "queue", m.queueComponent)
	router.HandleComponent(componentNamespace, "view", m.viewComponent)
	router.HandleComponent(componentNamespace, "reply", m.replyComponent)
	router.HandleComponent(componentNamespace, "close", m.closeComponent)
	router.HandleComponent(componentNamespace, "repair", m.repairComponent)
	router.HandleModal(componentNamespace, "reply-submit", m.submitReplyModal)
}

// SweepTranscripts deletes transcripts past their retention. Ticket
// timelines are kept.
func (m *Module) SweepTranscripts(ctx context.Context) error {
	_, err := m.service.PurgeExpiredTranscripts(ctx)
	return err
}
