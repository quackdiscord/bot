package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// The case the entry is about, when it is about a case, one of its
	// executions, or its appeal. CaseID is empty otherwise.
	CaseID              string
	CaseNumber          uint64
	TargetDiscordUserID string
	// RuleName is the template name frozen in the case snapshot.
	RuleName string
	// ActionType is the execution's action, for execution entries.
	ActionType ActionType
	// SelectedLevelName and SelectedOutcome describe the level chosen when
	// the case was created, such as "Timeout (24h)" or "Warning". They are
	// set only on case.create entries and say what was decided, not that
	// enforcement finished.
	SelectedLevelName, SelectedOutcome string
	// ReversalNoop is set on a succeeded reversal that found the punishment
	// already over, so nothing was sent to Discord.
	ReversalNoop bool
	// RetryExecutionID is set on a failed execution staff can still retry:
	// it is not dismissed, and it is a reversal or its case is still valid.
	RetryExecutionID string
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
	if err := m.describeCase(ctx, entry, &message); err != nil {
		return m.recordOutcome(ctx, entry, AuditActionMirrorFailed, AuditResultFailure, "case_details_unavailable")
	}
	err = m.sender.SendAuditMirror(ctx, message)
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

// describeCase fills in the case an entry is about, found through the case,
// execution, or appeal it names within the entry's guild. It adds only
// identifiers and the snapshotted decision, never evidence or context.
func (m *AuditMirror) describeCase(ctx context.Context, entry AuditLogEntry, message *AuditMirrorMessage) error {
	var caseID string
	var execution *CaseActionExecution
	switch entry.ResourceType {
	case "case":
		caseID = entry.ResourceID
	case "case_action_execution":
		var err error
		if execution, err = m.store.GetCaseActionExecution(ctx, entry.GuildID, entry.ResourceID); err != nil {
			return err
		}
		if execution != nil {
			caseID = execution.CaseID
		}
	case "appeal":
		appeal, err := m.store.GetAppealByID(ctx, entry.ResourceID)
		if err != nil {
			return err
		}
		if appeal != nil && appeal.GuildID == entry.GuildID && appeal.CaseID != nil {
			caseID = *appeal.CaseID
		}
	}
	if caseID == "" {
		return nil
	}
	item, err := m.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return err
	}
	if item == nil || item.GuildID != entry.GuildID {
		return nil
	}
	message.CaseID, message.CaseNumber = item.ID, item.CaseNumber
	message.TargetDiscordUserID = item.TargetDiscordUserID
	message.RuleName = snapshotRuleName(item.TemplateSnapshotJSON)
	if entry.Action == string(AuditActionCaseCreate) {
		message.SelectedLevelName, message.SelectedOutcome = selectedOutcome(item.TemplateSnapshotJSON)
	}
	if execution == nil {
		return nil
	}
	message.ActionType = execution.ActionType
	reversal := execution.ReversalOfExecutionID != nil
	if entry.Action == string(AuditActionActionSucceeded) && reversal {
		var metadata struct {
			ReversalNoop bool `json:"reversal_noop"`
		}
		message.ReversalNoop = json.Unmarshal([]byte(entry.MetadataJSON), &metadata) == nil && metadata.ReversalNoop
	}
	// A failed punishment on a voided case must not be retried, but a failed
	// reversal still needs one or the member stays punished.
	if entry.Action == string(AuditActionActionFailed) && execution.Status == ActionExecutionFailed &&
		execution.DismissedAt == nil && (reversal || item.Validity == CaseValidityValid) {
		message.RetryExecutionID = execution.ID
	}
	return nil
}

// selectedOutcome summarizes the level a case snapshot selected: its name
// and its outcome, such as "Timeout (24h)", or "Warning" for a level with no
// action. Snapshots without a selected level give empty strings.
func selectedOutcome(snapshotJSON string) (level, outcome string) {
	snapshot := parseTemplateSnapshot(snapshotJSON)
	if snapshot == nil || snapshot.SelectedLevel.ID == "" {
		return "", ""
	}
	outcomes := make([]string, 0, len(snapshot.Actions))
	for _, action := range snapshot.Actions {
		label := action.ActionType.Label()
		if seconds := action.TimeoutDurationSeconds; action.ActionType == ActionTimeoutUser && seconds > 0 {
			switch {
			case seconds%3600 == 0:
				label += fmt.Sprintf(" (%dh)", seconds/3600)
			case seconds%60 == 0:
				label += fmt.Sprintf(" (%dm)", seconds/60)
			default:
				label += fmt.Sprintf(" (%ds)", seconds)
			}
		}
		outcomes = append(outcomes, label)
	}
	if len(outcomes) == 0 {
		outcomes = append(outcomes, "Warning")
	}
	return snapshot.SelectedLevel.Name, strings.Join(outcomes, ", ")
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
