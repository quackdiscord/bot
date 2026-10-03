package honeypot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

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

// Batch sizes for one Sweep: incidents to recover, then bait messages to
// delete.
const (
	sweepRecoveries = 2
	sweepCleanups   = 8
)

// Module is the honeypot wired to Discord and the API. The app mounts its
// routes and /setup, registers its gateway handlers, requests its Intents,
// tells it about template changes, runs Sweep and RefreshWarnings as
// background loops, and runs its message pool between Start and Stop.
type Module struct {
	service       *Service
	registry      *modules.Registry
	guilds        *modules.Guilds
	session       *discordgo.Session
	templates     TemplateService
	templateCheck templateValidator
	channelCheck  channelValidator
	warnings      *warnings
	locks         *guildLocks
	pool          *modules.Pool[Message]

	// cleanupCtx bounds the bait-message deletes that follow each pooled
	// message. Stop cancels it first, so a slow Discord delete never holds
	// up the drain; the receipt stays due for Sweep.
	mu            sync.Mutex
	cleanupCtx    context.Context
	cancelCleanup context.CancelFunc
}

// New returns the honeypot module and registers its enablement check with
// registry. Triggers are stored in db, settings in registry, events audited
// to audit; session reaches Discord, core reads templates and saved cases,
// cases opens the resulting cases, and templates creates the setup template
// and names its punishments in the warning.
func New(db *gorm.DB, registry *modules.Registry, audit modules.Auditor, guilds *modules.Guilds, session *discordgo.Session, core CoreStore, cases CaseCreator, templates TemplateService) *Module {
	templateCheck := templateValidator{store: core}
	channelCheck := channelValidator{session: session, guilds: guilds}
	applier := caseApplier{cases: cases, store: core, session: session}
	service := NewService(registry, NewStore(db), audit, channelCheck, templateCheck, applier)
	locks := &guildLocks{}
	cleanupCtx, cancel := context.WithCancel(context.Background())
	m := &Module{
		service:       service,
		registry:      registry,
		guilds:        guilds,
		session:       session,
		templates:     templates,
		templateCheck: templateCheck,
		channelCheck:  channelCheck,
		warnings:      &warnings{session: session, service: service, policy: templates, guilds: guilds, locks: locks},
		locks:         locks,
		cleanupCtx:    cleanupCtx,
		cancelCleanup: cancel,
	}
	m.pool = modules.NewPool("honeypot", queueCapacity, queueWorkers, m.handle)
	registry.SetEnablementCheck(modules.Honeypots, m.checkEnablement)
	return m
}

// NewPool returns the bounded queue that runs trap messages through
// service, without the cleanup that follows each one in a Module.
func NewPool(service *Service) *modules.Pool[Message] {
	return modules.NewPool("honeypot", queueCapacity, queueWorkers, func(ctx context.Context, message Message) {
		handleMessage(ctx, service, message)
	})
}

// handle runs one trap message, then deletes whatever bait is already due,
// so the message usually disappears right away rather than on the next
// Sweep.
func (m *Module) handle(ctx context.Context, message Message) {
	handleMessage(ctx, m.service, message)
	m.mu.Lock()
	cleanupCtx := m.cleanupCtx
	m.mu.Unlock()
	if err := m.service.ProcessCleanups(cleanupCtx, 1); err != nil && cleanupCtx.Err() == nil {
		slog.WarnContext(ctx, "Honeypot message cleanup will retry", "guild_id", message.GuildID, "error_type", fmt.Sprintf("%T", err))
	}
}

// handleMessage runs one trap message, logging only unexpected failures.
// Message content is never logged.
func handleMessage(ctx context.Context, service *Service, message Message) {
	_, err := service.HandleMessage(ctx, message)
	if err == nil || errors.Is(err, ErrDuplicate) || errors.Is(err, ErrExempt) ||
		errors.Is(err, ErrNotTrigger) || errors.Is(err, ErrDisabled) {
		return
	}
	slog.ErrorContext(ctx, "Honeypot event failed",
		"guild_id", message.GuildID, "error_type", fmt.Sprintf("%T", err))
}

// MountHTTP mounts the honeypot routes.
func (m *Module) MountHTTP(mux modules.Mux) {
	RegisterRoutes(mux, m.service, modules.RequestActor)
}

// RegisterGateway subscribes the honeypot to new messages, deletions of its
// warning, and deletions of its trap channel or guild.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onMessageDelete)
	session.AddHandler(m.onMessageDeleteBulk)
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
func (m *Module) Start(ctx context.Context) {
	m.mu.Lock()
	m.cancelCleanup()
	m.cleanupCtx, m.cancelCleanup = context.WithCancel(context.WithoutCancel(ctx))
	m.mu.Unlock()
	m.pool.Start(ctx)
}

// Stop cancels in-flight bait deletes, then drains queued trap messages,
// giving up when ctx is done. Interrupted deletes stay due for Sweep after
// a restart.
func (m *Module) Stop(ctx context.Context) error {
	m.mu.Lock()
	m.cancelCleanup()
	m.mu.Unlock()
	return m.pool.Stop(ctx)
}

// Sweep is the honeypot's upkeep loop; run it every second. It finishes
// incidents a crash or restart interrupted, without ever punishing twice,
// and deletes bait messages whose incident has a saved case. Its first run
// at startup is the restart recovery.
func (m *Module) Sweep(ctx context.Context) error {
	_, recoverErr := m.service.RecoverPending(ctx, sweepRecoveries)
	return errors.Join(recoverErr, m.service.ProcessCleanups(ctx, sweepCleanups))
}

// RefreshWarnings keeps warning posts current; run it every second. Its
// first run queues every enabled guild's warning, so counts and wording
// catch up after a restart; then each run edits the warnings that are due,
// reposting any that were deleted. Failed deliveries back off and are
// logged rather than returned.
func (m *Module) RefreshWarnings(ctx context.Context) error {
	return m.warnings.run(ctx)
}

// HandleTemplateChange turns the honeypot off if a template edit or archive
// left its template unusable unattended. A storage failure changes nothing.
// It implements api.TemplateChangeHandler.
func (m *Module) HandleTemplateChange(ctx context.Context, guildID, templateID string) {
	if errors.Is(m.templateCheck.ValidateHoneypotTemplate(ctx, guildID, templateID), ErrTemplateUnavailable) {
		_ = m.service.HandleTemplateUnavailable(ctx, guildID, templateID)
	}
}
