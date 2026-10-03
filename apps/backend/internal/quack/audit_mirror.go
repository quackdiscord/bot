package quack

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"sync"
	"time"
)

// AuditMirrorMessage is an important audit entry, already redacted, ready to
// post in a guild's audit channel.
type AuditMirrorMessage struct {
	AuditEntryID       string
	DiscordGuildID     string
	ChannelDiscordID   string
	OccurredAt         time.Time
	ActorDiscordUserID string
	Action             string
	ResourceType       string
	ResourceID         string
	Result             AuditResult
	FailureReason      string
	RequestID          string
	CorrelationID      string
	MetadataJSON       string
}

// AuditMirrorWorker copies important audit entries to each guild's audit
// channel. It polls the audit log instead of hooking writes, so a Discord
// outage never blocks the operation being audited. Each delivery outcome is
// itself audited, which is also how the worker knows an entry is done.
type AuditMirrorWorker struct {
	store    AuditMirrorStore
	sender   AuditMirrorSender
	interval time.Duration
	batch    int
	pollMu   sync.Mutex
}

// NewAuditMirrorWorker returns a worker that polls every interval, or every
// five seconds when interval is not positive.
func NewAuditMirrorWorker(store AuditMirrorStore, sender AuditMirrorSender, interval time.Duration) *AuditMirrorWorker {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return &AuditMirrorWorker{store: store, sender: sender, interval: interval, batch: 50}
}

// Run polls until ctx is canceled. Failed polls are logged and retried on the
// next tick.
func (w *AuditMirrorWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		if err := w.PollOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Audit mirror poll failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// PollOnce mirrors one batch of pending entries. Concurrent calls run one at
// a time so an entry is never sent twice.
func (w *AuditMirrorWorker) PollOnce(ctx context.Context) error {
	w.pollMu.Lock()
	defer w.pollMu.Unlock()
	entries, err := w.store.ListPendingAuditMirrorEntries(ctx, w.batch)
	if err != nil {
		return err
	}
	var failures []error
	for i := range entries {
		if err := w.process(ctx, entries[i]); err != nil {
			failures = append(failures, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(failures...)
}

func (w *AuditMirrorWorker) process(ctx context.Context, entry AuditLogEntry) error {
	settings, err := w.store.GetGuildSettings(ctx, entry.GuildID)
	if err != nil {
		return w.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "settings_unavailable", nil)
	}
	if settings == nil || strings.TrimSpace(settings.AuditMirrorChannelDiscordID) == "" {
		return w.recordOutcome(ctx, entry, AuditActionMirrorSkipped, AuditResultSuccess, "not_configured", nil)
	}
	guild, err := w.store.GetGuildByID(ctx, entry.GuildID)
	if err != nil || guild == nil {
		return w.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "guild_unavailable", nil)
	}
	if w.sender == nil {
		return w.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "sender_unavailable", nil)
	}
	message := AuditMirrorMessage{
		AuditEntryID:       entry.ID,
		DiscordGuildID:     guild.DiscordGuildID,
		ChannelDiscordID:   settings.AuditMirrorChannelDiscordID,
		OccurredAt:         entry.CreatedAt,
		ActorDiscordUserID: entry.ActorDiscordUserID,
		Action:             entry.Action,
		ResourceType:       entry.ResourceType,
		ResourceID:         entry.ResourceID,
		Result:             entry.Result,
		FailureReason:      entry.FailureReason,
		RequestID:          entry.RequestID,
		CorrelationID:      entry.CorrelationID,
		MetadataJSON:       RedactAuditMetadata(entry.MetadataJSON),
	}
	err = w.sender.SendAuditMirror(ctx, message)
	switch {
	case err == nil:
		return w.recordOutcome(ctx, entry, AuditActionMirrorDelivered, AuditResultSuccess, "", nil)
	case errors.Is(err, ErrAuditMirrorChannelUnavailable):
		if err := w.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "channel_unavailable", nil); err != nil {
			return err
		}
		// The channel is gone, so stop trying it for every later entry.
		repair := &AuditLogEntry{
			GuildID:            entry.GuildID,
			ActorDiscordUserID: systemActorID,
			Source:             AuditSourceSystem,
			Action:             string(AuditActionMirrorRepaired),
			ResourceType:       "guild_settings",
			ResourceID:         settings.ID,
			Result:             AuditResultSuccess,
			CorrelationID:      entry.CorrelationID,
			MetadataJSON:       auditMirrorMetadata(entry.ID, map[string]any{"cleared_channel_reference": true}),
		}
		_, err := w.store.ClearGuildChannelReferences(ctx, entry.GuildID, settings.AuditMirrorChannelDiscordID, repair)
		return err
	default:
		return w.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "delivery_failed", nil)
	}
}

func (w *AuditMirrorWorker) recordOutcome(ctx context.Context, original AuditLogEntry, action AuditAction, result AuditResult, failure string, extra map[string]any) error {
	return recordAudit(ctx, w.store, &AuditLogEntry{
		GuildID:            original.GuildID,
		ActorDiscordUserID: systemActorID,
		Source:             AuditSourceSystem,
		Action:             string(action),
		ResourceType:       "audit_entry",
		ResourceID:         original.ID,
		Result:             result,
		FailureReason:      failure,
		RequestID:          original.RequestID,
		CorrelationID:      original.CorrelationID,
		MetadataJSON:       auditMirrorMetadata(original.ID, extra),
	})
}

func auditMirrorMetadata(originalID string, extra map[string]any) string {
	metadata := map[string]any{"audit_entry_id": originalID}
	maps.Copy(metadata, extra)
	return marshalJSONObject(metadata)
}
