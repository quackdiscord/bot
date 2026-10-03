package quack

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// auditMirrorBatch is how many pending entries one poll mirrors.
const auditMirrorBatch = 50

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

// AuditMirror copies important audit entries to each guild's audit
// channel. It polls the audit log (the worker calls PollOnce on a timer)
// instead of hooking writes, so a Discord outage never blocks the operation
// being audited. Each delivery outcome is itself audited, which is also how
// the mirror knows an entry is done.
type AuditMirror struct {
	store  AuditMirrorStore
	sender AuditMirrorSender
	pollMu sync.Mutex
}

// NewAuditMirror returns an AuditMirror that reads store and sends through
// sender.
func NewAuditMirror(store AuditMirrorStore, sender AuditMirrorSender) *AuditMirror {
	return &AuditMirror{store: store, sender: sender}
}

// PollOnce mirrors one batch of pending entries. Concurrent calls run one at
// a time so an entry is never sent twice.
func (m *AuditMirror) PollOnce(ctx context.Context) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	entries, err := m.store.ListPendingAuditMirrorEntries(ctx, auditMirrorBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		if err := m.mirror(ctx, entry); err != nil {
			failures = append(failures, err)
			if ctx.Err() != nil {
				break
			}
		}
	}
	return errors.Join(failures...)
}

// mirror sends one entry and records the outcome. When Discord reports the
// channel gone, the channel is cleared from settings so later entries are
// skipped instead of failing one by one.
func (m *AuditMirror) mirror(ctx context.Context, entry AuditLogEntry) error {
	settings, err := m.store.GetGuildSettings(ctx, entry.GuildID)
	if err != nil {
		return m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "settings_unavailable")
	}
	if settings == nil || strings.TrimSpace(settings.AuditMirrorChannelDiscordID) == "" {
		return m.recordOutcome(ctx, entry, AuditActionMirrorSkipped, AuditResultSuccess, "not_configured")
	}
	guild, err := m.store.GetGuildByID(ctx, entry.GuildID)
	if err != nil || guild == nil {
		return m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "guild_unavailable")
	}
	if m.sender == nil {
		return m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "sender_unavailable")
	}
	err = m.sender.SendAuditMirror(ctx, AuditMirrorMessage{
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
	})
	switch {
	case err == nil:
		return m.recordOutcome(ctx, entry, AuditActionMirrorDelivered, AuditResultSuccess, "")
	case errors.Is(err, ErrAuditMirrorChannelUnavailable):
		if err := m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "channel_unavailable"); err != nil {
			return err
		}
		repair := &AuditLogEntry{
			GuildID:            entry.GuildID,
			ActorDiscordUserID: systemActorID,
			Source:             AuditSourceSystem,
			Action:             string(AuditActionMirrorRepaired),
			ResourceType:       "guild_settings",
			ResourceID:         settings.ID,
			Result:             AuditResultSuccess,
			CorrelationID:      entry.CorrelationID,
			MetadataJSON: marshalJSONObject(map[string]any{
				"audit_entry_id":            entry.ID,
				"cleared_channel_reference": true,
			}),
		}
		_, err := m.store.ClearGuildChannelReferences(ctx, entry.GuildID, settings.AuditMirrorChannelDiscordID, repair)
		return err
	default:
		return m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "delivery_failed")
	}
}

// recordOutcome audits what happened to original. The outcome entry is what
// takes original off the pending list.
func (m *AuditMirror) recordOutcome(ctx context.Context, original AuditLogEntry, action AuditAction, result AuditResult, failure string) error {
	return recordAudit(ctx, m.store, &AuditLogEntry{
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
		MetadataJSON:       marshalJSONObject(map[string]any{"audit_entry_id": original.ID}),
	})
}
