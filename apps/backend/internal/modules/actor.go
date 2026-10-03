package modules

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/quackdiscord/bot/internal/quack"
)

// Actor is the caller of a module operation and what they may do, taken
// from live Discord permissions.
type Actor struct {
	GuildID, DiscordUserID string
	// CanManage is Manage Guild: configure the module and repair it.
	CanManage bool
	// CanModerate is the ticket.resolve permission: work the ticket queue.
	CanModerate bool
}

// ActorFor maps a live staff context to a module actor.
func ActorFor(staff *quack.GuildStaffContext) Actor {
	return Actor{
		GuildID:       staff.Guild.ID,
		DiscordUserID: staff.ActorDiscordUserID,
		CanManage:     staff.Can(quack.PermissionActionGuildSettingsWrite),
		CanModerate:   staff.Can(quack.PermissionActionTicketResolve),
	}
}

// ActorResolver returns the actor behind an HTTP request.
type ActorResolver func(*http.Request) (Actor, error)

// RequestActor is the ActorResolver for routes mounted on api.ModuleMux,
// whose guild middleware has already put live staff context on the request.
func RequestActor(r *http.Request) (Actor, error) {
	staff := quack.StaffFromContext(r.Context())
	if staff == nil || staff.Guild == nil {
		return Actor{}, errors.New("live guild context is unavailable")
	}
	return ActorFor(staff), nil
}

// ErrUnknownGuild is returned for a guild Quack has no record of.
var ErrUnknownGuild = errors.New("guild is not registered")

// GuildStore looks up guild records.
type GuildStore interface {
	GetGuildByID(ctx context.Context, guildID string) (*quack.Guild, error)
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*quack.Guild, error)
}

// Guilds maps between Discord guild IDs, which gateway events and Discord
// calls use, and Quack's internal guild IDs, which module tables use.
type Guilds struct{ store GuildStore }

// NewGuilds returns a Guilds over store.
func NewGuilds(store GuildStore) *Guilds { return &Guilds{store: store} }

// InternalID returns the internal ID of an active guild. Events from guilds
// Quack has left resolve to ErrUnknownGuild and are dropped.
func (g *Guilds) InternalID(ctx context.Context, discordGuildID string) (string, error) {
	guild, err := g.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil {
		return "", err
	}
	if guild == nil || !guild.IsActive {
		return "", ErrUnknownGuild
	}
	return guild.ID, nil
}

// InternalIDAny is InternalID for departure cleanup: it also resolves an
// inactive guild, since the core lifecycle handler may already have marked
// it inactive.
func (g *Guilds) InternalIDAny(ctx context.Context, discordGuildID string) (string, error) {
	guild, err := g.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil {
		return "", err
	}
	if guild == nil {
		return "", ErrUnknownGuild
	}
	return guild.ID, nil
}

// DiscordID returns the Discord ID of an active guild.
func (g *Guilds) DiscordID(ctx context.Context, guildID string) (string, error) {
	guild, err := g.store.GetGuildByID(ctx, guildID)
	if err != nil {
		return "", err
	}
	if guild == nil || !guild.IsActive {
		return "", ErrUnknownGuild
	}
	return guild.DiscordGuildID, nil
}

// AuditEvent is the outcome of one module operation.
type AuditEvent struct {
	GuildID, ActorDiscordUserID      string
	Action, ResourceType, ResourceID string
	// Result is "success", "failure", or "denied".
	Result, FailureReason, MetadataJSON string
}

// Auditor records module audit events.
type Auditor interface {
	RecordModuleAudit(ctx context.Context, event AuditEvent) error
}

// AuditStore appends to the core audit log.
type AuditStore interface {
	CreateAuditLogEntry(ctx context.Context, entry *quack.AuditLogEntry) error
}

// AuditLog is the Auditor every module uses: it appends module events to
// the core audit log, so staff see them next to case history.
type AuditLog struct{ store AuditStore }

// NewAuditLog returns an AuditLog that writes to store.
func NewAuditLog(store AuditStore) *AuditLog { return &AuditLog{store: store} }

// RecordModuleAudit appends event. Its source comes from the context (API
// or Discord), or is the honeypot for honeypot automation.
func (a *AuditLog) RecordModuleAudit(ctx context.Context, event AuditEvent) error {
	result := quack.AuditResult(event.Result)
	switch result {
	case quack.AuditResultSuccess, quack.AuditResultFailure, quack.AuditResultDenied:
	default:
		return errors.New("module audit result is invalid")
	}
	requestID, correlationID := quack.TraceIDsFromContext(ctx)
	return a.store.CreateAuditLogEntry(ctx, &quack.AuditLogEntry{
		GuildID:            event.GuildID,
		ActorDiscordUserID: event.ActorDiscordUserID,
		Source:             quack.AuditSourceForModuleAction(ctx, event.Action),
		Action:             event.Action,
		ResourceType:       event.ResourceType,
		ResourceID:         event.ResourceID,
		Result:             result,
		FailureReason:      event.FailureReason,
		RequestID:          requestID,
		CorrelationID:      correlationID,
		MetadataJSON:       event.MetadataJSON,
	})
}

// Audit logs a module operation and, when auditor is set, records it in the
// audit log. A failed audit write is logged, never returned: the operation
// itself has already happened.
func Audit(ctx context.Context, auditor Auditor, module string, event AuditEvent) {
	level := slog.LevelInfo
	if event.Result != "success" {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Module operation completed",
		"module", module, "guild_id", event.GuildID, "action", event.Action, "result", event.Result)
	if auditor == nil {
		return
	}
	if event.MetadataJSON == "" {
		event.MetadataJSON = "{}"
	}
	if err := auditor.RecordModuleAudit(ctx, event); err != nil {
		slog.ErrorContext(ctx, "Module audit could not be recorded",
			"module", module, "guild_id", event.GuildID, "action", event.Action)
	}
}
