package modules

import (
	"context"
	"errors"
	"log/slog"

	"github.com/quackdiscord/bot/internal/quack"
)

// AuditEvent is the outcome of one module operation.
type AuditEvent struct {
	GuildID, ActorDiscordUserID      string
	Action, ResourceType, ResourceID string
	// Result is "success" or "failure". Modules do not audit refusals of
	// the person acting.
	Result, FailureReason, MetadataJSON string
}

// Auditor records module audit events. Module tests substitute a recorder.
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
	case quack.AuditResultSuccess, quack.AuditResultFailure:
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
			"module", module, "guild_id", event.GuildID, "action", event.Action, "error", err)
	}
}
