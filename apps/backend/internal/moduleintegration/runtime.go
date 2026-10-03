// Package moduleintegration composes optional v5 modules at process boundaries
// without allowing their storage or delivery lifecycles into the moderation core.
package moduleintegration

import (
	"context"
	"errors"
	"sync"
	"time"

	"log/slog"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"gorm.io/gorm"
)

const (
	transcriptSweepInterval = time.Hour
	loggingQueueCapacity    = 1000
	loggingQueueWorkers     = 2
	honeypotQueueCapacity   = 256
	honeypotQueueWorkers    = 2
	appealDispatchInterval  = 5 * time.Second
	appealDispatchBatch     = 50
)

// Runtime owns the optional-module services and their process-scoped workers.
type Runtime struct {
	Tickets          *tickets.Service
	TicketDiscord    *tickets.DiscordAdapter
	Logging          *generallogging.Service
	LoggingQueue     *generallogging.DeliveryQueue
	Honeypot         *honeypot.Service
	HoneypotDiscord  *honeypot.DiscordAdapter
	HoneypotRuntime  *honeypot.Runtime
	AuditMirror      *quack.AuditMirrorWorker
	Appeals          *quack.AppealService
	AppealDispatcher *quack.AppealNotificationDispatcher

	db                  *gorm.DB
	registry            *modules.Registry
	session             *discordgo.Session
	resolver            guildResolver
	repository          templateReader
	services            *quack.Services
	cancel              context.CancelFunc
	bulk                chan bulkDeleteEvent
	bulkMu              sync.RWMutex
	bulkWG              sync.WaitGroup
	sweepWG             sync.WaitGroup
	mirrorWG            sync.WaitGroup
	appealWG            sync.WaitGroup
	ticketRepairMu      sync.Mutex
	ticketRepairPending map[string]struct{}
	ticketRepairRunning bool
	closed              bool
	closeOnce           sync.Once
	closeDone           chan struct{}
}

// bulkDeleteEvent carries one bounded cache-aware bulk deletion job.
type bulkDeleteEvent struct {
	guildID, channelID string
	messageIDs         []string
}

// New constructs the shared registry, immutable audit adapter, module stores,
// Discord adapters, and bounded general-logging delivery workers. bot sends
// the audit mirror and appeal notifications.
func New(ctx context.Context, repositories *store.Store, bot *discord.Bot, services *quack.Services) (*Runtime, error) {
	if repositories == nil || repositories.DB() == nil {
		return nil, errors.New("optional module database is not configured")
	}
	if services == nil || services.Cases == nil || services.Appeals == nil {
		return nil, errors.New("optional module core services are not configured")
	}
	session := bot.Session

	registry, err := modules.NewRegistry(
		modules.NewSQLSettingsStore(repositories.DB()),
		tickets.Descriptor(),
		generallogging.Descriptor(),
		honeypot.Descriptor(),
	)
	if err != nil {
		return nil, err
	}
	auditor := moduleAuditor{repository: repositories}
	ticketService := tickets.NewService(registry, tickets.NewStore(repositories.DB()), auditor)
	resolver := guildResolver{db: repositories.DB()}
	ticketClient := ticketDiscordClient{session: session, resolver: resolver}
	loggingClient := loggingDiscordClient{bot: bot, resolver: resolver}
	loggingService := generallogging.NewService(registry, auditor, loggingClient, nil)
	honeypotTemplates := honeypotTemplateValidator{repository: repositories}
	honeypotChannels := honeypotChannelValidator{session: session, resolver: resolver}
	honeypotService := honeypot.NewService(registry, honeypot.NewStore(repositories.DB()), auditor, honeypotChannels, honeypotTemplates, honeypotCaseApplier{cases: services.Cases})
	honeypotDiscord := honeypot.NewDiscordAdapter(honeypotService)
	appeals := services.Appeals
	appealNotifier := discord.NewAppealNotifier(bot, appealStaffChannelResolver{repository: repositories, validator: bot})
	appealDispatcher := quack.NewAppealNotificationDispatcher(repositories, appealNotifier)
	workerCtx, cancel := context.WithCancel(ctx)
	auditMirror := quack.NewAuditMirrorWorker(repositories, bot, 0)

	runtime := &Runtime{
		Tickets:          ticketService,
		TicketDiscord:    tickets.NewDiscordAdapter(ticketService, ticketClient),
		Logging:          loggingService,
		LoggingQueue:     generallogging.NewDeliveryQueue(workerCtx, loggingService, loggingQueueCapacity, loggingQueueWorkers),
		Honeypot:         honeypotService,
		HoneypotDiscord:  honeypotDiscord,
		HoneypotRuntime:  honeypot.NewRuntime(workerCtx, honeypotDiscord, honeypotQueueCapacity, honeypotQueueWorkers),
		AuditMirror:      auditMirror,
		Appeals:          appeals,
		AppealDispatcher: appealDispatcher,
		db:               repositories.DB(),
		registry:         registry,
		session:          session,
		resolver:         resolver,
		repository:       repositories,
		services:         services,
		cancel:           cancel,
		bulk:             make(chan bulkDeleteEvent, loggingQueueCapacity),
		closeDone:        make(chan struct{}),
	}
	for range loggingQueueWorkers {
		runtime.bulkWG.Add(1)
		go runtime.runBulkDeletes(workerCtx)
	}
	runtime.sweepWG.Add(1)
	go func() {
		defer runtime.sweepWG.Done()
		runtime.runTranscriptSweep(workerCtx)
	}()
	if runtime.AuditMirror != nil {
		runtime.mirrorWG.Add(1)
		go func() {
			defer runtime.mirrorWG.Done()
			runtime.AuditMirror.Run(workerCtx)
		}()
	}
	runtime.appealWG.Add(1)
	go func() {
		defer runtime.appealWG.Done()
		runtime.runAppealNotifications(workerCtx)
	}()
	return runtime, nil
}

// Close cancels periodic work and drains already accepted logging deliveries.
func (r *Runtime) Close() {
	_ = r.CloseContext(context.Background())
}

// CloseContext cancels module work, stops accepting deliveries, and waits only
// through the caller's graceful-shutdown deadline.
func (r *Runtime) CloseContext(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.bulkMu.Lock()
	if r.closeDone == nil {
		r.closeDone = make(chan struct{})
	}
	r.bulkMu.Unlock()
	r.closeOnce.Do(func() {
		go func() {
			r.bulkMu.Lock()
			r.closed = true
			if r.bulk != nil {
				close(r.bulk)
			}
			r.bulkMu.Unlock()
			r.bulkWG.Wait()
			if r.HoneypotRuntime != nil {
				r.HoneypotRuntime.Close()
			}
			if r.LoggingQueue != nil {
				r.LoggingQueue.Close()
			}
			if r.cancel != nil {
				r.cancel()
			}
			r.sweepWG.Wait()
			r.mirrorWG.Wait()
			r.appealWG.Wait()
			close(r.closeDone)
		}()
	})
	select {
	case <-r.closeDone:
		return nil
	case <-ctx.Done():
		if r.cancel != nil {
			r.cancel()
		}
		return ctx.Err()
	}
}

// runAppealNotifications drains the durable appeal outbox in bounded batches
// without coupling Discord delivery to moderation transactions.
func (r *Runtime) runAppealNotifications(ctx context.Context) {
	ticker := time.NewTicker(appealDispatchInterval)
	defer ticker.Stop()
	for {
		if err := r.AppealDispatcher.DispatchPending(ctx, appealDispatchBatch); err != nil && !errors.Is(err, context.Canceled) {
			slog.Error("Failed to dispatch appeal notifications", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// appealStaffChannelResolver reuses the configured staff-only audit channel as
// the appeal queue notification destination.
type appealStaffChannelResolver struct {
	repository interface {
		GetGuildSettings(ctx context.Context, guildID string) (*quack.GuildSettings, error)
		GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error)
	}
	validator interface {
		ValidateStaffChannel(context.Context, string, string) error
	}
}

// AppealStaffChannel resolves the current staff-only destination for a guild.
func (r appealStaffChannelResolver) AppealStaffChannel(ctx context.Context, guildID string) (string, error) {
	if r.repository == nil {
		return "", errors.New("appeal staff channel repository is not configured")
	}
	settings, err := r.repository.GetGuildSettings(ctx, guildID)
	if err != nil || settings == nil {
		return "", err
	}
	if r.validator == nil {
		return "", errors.New("appeal staff channel validator is unavailable")
	}
	guild, err := r.repository.GetGuildByID(ctx, guildID)
	if err != nil || guild == nil {
		return "", errors.New("appeal guild is unavailable")
	}
	if err := r.validator.ValidateStaffChannel(ctx, guild.DiscordGuildID, settings.AuditMirrorChannelDiscordID); err != nil {
		return "", err
	}
	return settings.AuditMirrorChannelDiscordID, nil
}

// runBulkDeletes drains cache-aware bulk deletion work independently of case actions.
func (r *Runtime) runBulkDeletes(ctx context.Context) {
	defer r.bulkWG.Done()
	for event := range r.bulk {
		_ = r.Logging.HandleBulkDelete(ctx, event.guildID, event.channelID, event.messageIDs)
	}
}

// submitBulkDelete sheds work when the isolated logging queue is saturated.
func (r *Runtime) submitBulkDelete(event bulkDeleteEvent) {
	r.bulkMu.RLock()
	defer r.bulkMu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.bulk <- event:
	default:
		// General logging is explicitly shed rather than delaying moderation.
	}
}

// runTranscriptSweep purges expired private content promptly at startup and on
// a bounded interval while leaving ticket timelines intact.
func (r *Runtime) runTranscriptSweep(ctx context.Context) {
	r.purgeTranscripts(ctx)
	ticker := time.NewTicker(transcriptSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.purgeTranscripts(ctx)
		}
	}
}

// purgeTranscripts reports cleanup failure operationally without terminating
// unrelated moderation or logging workers.
func (r *Runtime) purgeTranscripts(ctx context.Context) {
	if _, err := r.Tickets.PurgeExpiredTranscripts(ctx); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Failed to purge expired ticket transcripts", "error", err)
	}
}

// moduleAuditor adapts module outcomes into the append-only core audit store.
type moduleAuditor struct {
	repository interface {
		CreateAuditLogEntry(context.Context, *quack.AuditLogEntry) error
	}
}

// RecordModuleAudit appends one module event and never routes general-log
// payload delivery into audit history.
func (a moduleAuditor) RecordModuleAudit(ctx context.Context, event modules.AuditEvent) error {
	if a.repository == nil {
		return errors.New("module audit repository is not configured")
	}
	result := quack.AuditResult(event.Result)
	switch result {
	case quack.AuditResultSuccess, quack.AuditResultFailure, quack.AuditResultDenied:
	default:
		return errors.New("module audit result is invalid")
	}
	requestID, correlationID := quack.TraceIDsFromContext(ctx)
	return a.repository.CreateAuditLogEntry(ctx, &quack.AuditLogEntry{
		GuildID: event.GuildID, ActorDiscordUserID: event.ActorDiscordUserID,
		Source: quack.AuditSourceForModuleAction(ctx, event.Action), Action: event.Action,
		ResourceType: event.ResourceType, ResourceID: event.ResourceID,
		Result: result, FailureReason: event.FailureReason,
		RequestID: requestID, CorrelationID: correlationID, MetadataJSON: event.MetadataJSON,
	})
}

// guildResolver translates Discord transport identities into Quack's internal
// guild key without exposing persistence to feature modules.
type guildResolver struct{ db *gorm.DB }

// internalID returns the active internal guild identity for a Discord guild.
func (r guildResolver) internalID(ctx context.Context, discordGuildID string) (string, error) {
	var guild quack.Guild
	result := r.db.WithContext(ctx).Where("discord_guild_id = ? AND is_active = ?", discordGuildID, true).Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("active guild is not registered")
	}
	return guild.ID, nil
}

// internalIDAny resolves preserved guild identity for departure cleanup even
// when another gateway handler has already marked the guild inactive.
func (r guildResolver) internalIDAny(ctx context.Context, discordGuildID string) (string, error) {
	var guild quack.Guild
	result := r.db.WithContext(ctx).Where("discord_guild_id = ?", discordGuildID).Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("guild is not registered")
	}
	return guild.ID, nil
}

// discordID returns the Discord guild identity for an internal module key.
func (r guildResolver) discordID(ctx context.Context, guildID string) (string, error) {
	var guild quack.Guild
	result := r.db.WithContext(ctx).Where("id = ? AND is_active = ?", guildID, true).Limit(1).Find(&guild)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", errors.New("active guild is not registered")
	}
	return guild.DiscordGuildID, nil
}
