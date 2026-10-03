package tickets

import (
	"context"
	"sync"

	"github.com/bwmarrin/discordgo"
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
// registers its components, gateway handlers, and routes, and runs
// SweepTranscripts periodically.
type Module struct {
	service  *Service
	store    *Store
	discord  *DiscordAdapter
	channels channels
	guilds   *modules.Guilds
	staff    StaffResolver

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
	RegisterRoutes(mux, m.service, modules.RequestActor)
}

// SweepTranscripts deletes transcripts past their retention. Ticket
// timelines are kept.
func (m *Module) SweepTranscripts(ctx context.Context) error {
	_, err := m.service.PurgeExpiredTranscripts(ctx)
	return err
}
