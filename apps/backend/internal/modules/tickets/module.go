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
	adapter  *DiscordAdapter
	channels channels
	guilds   *modules.Guilds
	staff    StaffResolver

	// setupLocks allows one /setup tickets per guild at a time.
	setupLocks keyedLocks

	// repairMu guards the thread-repair queue; see repairThreads.
	repairMu      sync.Mutex
	repairPending map[string]struct{}
	repairRunning bool
}

// New returns the tickets module. Tickets are stored in db, switched on in
// registry, audited to audit, and run in Discord through session; staff
// resolves live authority for button presses. It installs the tickets
// enablement check on registry, so the settings API switches tickets on
// only with a working setup.
func New(db *gorm.DB, registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, session *discordgo.Session, staff StaffResolver) *Module {
	store := NewStore(db)
	service := NewService(registry, store, audit)
	channels := channels{session: session, guilds: guilds}
	m := &Module{
		service:       service,
		store:         store,
		adapter:       NewDiscordAdapter(service, channels),
		channels:      channels,
		guilds:        guilds,
		staff:         staff,
		repairPending: make(map[string]struct{}),
	}
	registry.SetEnablementCheck(modules.Tickets, m.checkEnablement)
	return m
}

// MountHTTP mounts the ticket routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, m.adapter, modules.RequestActor)
}

// RegisterGateway subscribes tickets to the gateway events it needs: ticket
// messages for the journal, member and role changes that can change who may
// stay in a thread, and deleted channels.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onGuildCreate)
	session.AddHandler(m.onMemberUpdate)
	session.AddHandler(m.onRoleUpdate)
	session.AddHandler(m.onRoleDelete)
	session.AddHandler(m.onChannelDelete)
}

// Intents returns the gateway intents tickets need: guild messages with
// their content for the journal, and members for thread membership repair.
// They are requested whether or not any guild has tickets on, so /setup
// tickets works without a restart.
func (m *Module) Intents() discordgo.Intent {
	return discordgo.IntentGuilds |
		discordgo.IntentGuildMembers |
		discordgo.IntentGuildMessages |
		discordgo.IntentMessageContent
}

// RegisterComponents installs /setup tickets and the ticket buttons and
// modal on router. The actions are baked into posted messages, so they must
// never be renamed.
func (m *Module) RegisterComponents(router *discord.Router) {
	router.HandleSetup("tickets", m.Setup)
	for action, handler := range map[string]discord.Handler{
		"open":         m.openComponent,
		"queue":        m.queueComponent,
		"view":         m.viewComponent,
		"close":        m.closeComponent,
		"repair":       m.repairComponent,
		"queuefix":     m.queueFixComponent,
		"queueadopt":   m.queueAdoptComponent,
		"queueretry":   m.queueRetryComponent,
		"queueretryok": m.queueRetryConfirmed,
	} {
		router.HandleComponent(componentNamespace, action, handler)
	}
	router.HandleModal(componentNamespace, "queueadopt", m.queueAdoptSubmit)
}

// SweepTranscripts deletes transcripts and journaled message text past
// their retention. Ticket timelines are kept.
func (m *Module) SweepTranscripts(ctx context.Context) error {
	_, err := m.service.PurgeExpiredTranscripts(ctx)
	return err
}
