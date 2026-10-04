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

// auditMirrorBatch is how many due deliveries one poll claims.
const auditMirrorBatch = 50

// auditMirrorBackoff is how long a delivery waits after its nth failed try
// (auditMirrorBackoff[n-1]). One more failure after the last step gives up,
// about eight and a half hours after the first.
var auditMirrorBackoff = []time.Duration{
	time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour,
}

// auditMirrorMaxAttempts is how many failed tries a delivery gets before it
// is marked failed for good.
var auditMirrorMaxAttempts = len(auditMirrorBackoff) + 1

// AuditMirrorDeliveryStatus is where an important audit entry is on its way
// to the guild's audit channel.
type AuditMirrorDeliveryStatus string

// Audit mirror delivery statuses. Pending, claimed, and sending are in
// flight; the rest are final.
const (
	// AuditMirrorPending waits for its next attempt time.
	AuditMirrorPending AuditMirrorDeliveryStatus = "pending"
	// AuditMirrorClaimed is leased by a poll that has not sent it yet. A
	// lapsed claim is due again.
	AuditMirrorClaimed AuditMirrorDeliveryStatus = "claimed"
	// AuditMirrorSending may already be in Discord. It is never claimed
	// again: a lapsed send fails as delivery_outcome_unknown.
	AuditMirrorSending AuditMirrorDeliveryStatus = "sending"
	// AuditMirrorDelivered was posted.
	AuditMirrorDelivered AuditMirrorDeliveryStatus = "delivered"
	// AuditMirrorSkipped had no channel to go to by the time it was due.
	AuditMirrorSkipped AuditMirrorDeliveryStatus = "skipped"
	// AuditMirrorFailed was given up on.
	AuditMirrorFailed AuditMirrorDeliveryStatus = "failed"
)

// AuditMirrorDelivery is a claimed delivery: the entry to send, how many
// tries have failed so far, and the lease that lets the claimer settle it.
type AuditMirrorDelivery struct {
	Entry      AuditLogEntry
	Attempts   int
	LeaseToken string
}

// CompleteAuditMirrorDeliveryParams settles a claimed or sending delivery.
type CompleteAuditMirrorDeliveryParams struct {
	AuditEntryID, LeaseToken string
	// Status is delivered, skipped, failed (given up), or pending to try
	// again at NextAttemptAt.
	Status        AuditMirrorDeliveryStatus
	Attempts      int
	NextAttemptAt time.Time
	// LastError is a short failure classification, never a raw error.
	LastError          string
	DeliveredMessageID string
	// GiveUpAudit goes with Status failed. The store writes it unless the
	// guild already has a delivery that failed since its last successful
	// one, so an outage is audited once rather than once per entry.
	GiveUpAudit *AuditLogEntry
}

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
// channel. The store queues a delivery row with each important entry; the
// worker calls PollOnce on a timer to send due rows, so a Discord outage
// never blocks the operation being audited. Delivery state stays out of the
// audit log, which gets an entry only when a dead channel is cleared or
// delivery is given up on.
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

// PollOnce claims a batch of due deliveries and sends them. Concurrent calls
// run one at a time; the store's leases keep separate processes apart.
// Delivery failures are recorded on the rows, not returned. Once a send to
// a guild fails, the rest of that guild's batch counts the same failure
// without calling Discord again.
func (m *AuditMirror) PollOnce(ctx context.Context) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	deliveries, err := m.store.ClaimAuditMirrorDeliveries(ctx, auditMirrorBatch)
	if err != nil {
		return err
	}
	failing := map[string]string{}
	var failures []error
	for _, delivery := range deliveries {
		// Unsent claims left behind lapse and are claimed again later.
		if ctx.Err() != nil {
			break
		}
		var err error
		if reason, ok := failing[delivery.Entry.GuildID]; ok {
			err = m.retry(ctx, delivery, reason)
		} else {
			err = m.mirror(ctx, delivery, failing)
		}
		if err != nil && !errors.Is(err, ErrAuditMirrorLeaseLost) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// mirror sends one delivery and records the outcome. When Discord reports
// the channel gone, the channel is cleared from settings so the guild's
// later deliveries are skipped instead of failing one by one. A failed send
// marks the guild in failing.
func (m *AuditMirror) mirror(ctx context.Context, delivery AuditMirrorDelivery, failing map[string]string) error {
	entry := delivery.Entry
	settings, err := m.store.GetGuildSettings(ctx, entry.GuildID)
	if err != nil {
		return m.retry(ctx, delivery, "settings_unavailable")
	}
	if settings == nil || strings.TrimSpace(settings.AuditMirrorChannelDiscordID) == "" {
		return m.settle(ctx, delivery, AuditMirrorSkipped, "not_configured", "")
	}
	guild, err := m.store.GetGuildByID(ctx, entry.GuildID)
	if err != nil || guild == nil {
		return m.retry(ctx, delivery, "guild_unavailable")
	}
	if m.sender == nil {
		return m.retry(ctx, delivery, "sender_unavailable")
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
		return m.retry(ctx, delivery, "case_details_unavailable")
	}
	if err := m.store.BeginAuditMirrorDelivery(ctx, entry.ID, delivery.LeaseToken); err != nil {
		return err
	}
	messageID, err := m.sender.SendAuditMirror(ctx, message)
	switch {
	case err == nil:
		return m.settle(ctx, delivery, AuditMirrorDelivered, "", messageID)
	case errors.Is(err, ErrAuditMirrorChannelUnavailable):
		if err := m.settle(ctx, delivery, AuditMirrorSkipped, "channel_unavailable", ""); err != nil {
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
		failing[entry.GuildID] = "delivery_failed"
		return m.retry(ctx, delivery, "delivery_failed")
	}
}

// retry counts a failed try at delivery and schedules the next one after
// auditMirrorBackoff, or gives up once auditMirrorMaxAttempts have failed.
// Giving up audits audit_mirror.failed, at most once per guild outage.
func (m *AuditMirror) retry(ctx context.Context, delivery AuditMirrorDelivery, reason string) error {
	attempts := delivery.Attempts + 1
	params := CompleteAuditMirrorDeliveryParams{
		AuditEntryID: delivery.Entry.ID,
		LeaseToken:   delivery.LeaseToken,
		Attempts:     attempts,
		LastError:    reason,
	}
	if attempts < auditMirrorMaxAttempts {
		params.Status = AuditMirrorPending
		params.NextAttemptAt = time.Now().UTC().Add(auditMirrorBackoff[attempts-1])
		return m.store.CompleteAuditMirrorDelivery(ctx, params)
	}
	entry := delivery.Entry
	params.Status = AuditMirrorFailed
	params.GiveUpAudit = &AuditLogEntry{
		GuildID:            entry.GuildID,
		ActorDiscordUserID: systemActorID,
		Source:             AuditSourceSystem,
		Action:             string(AuditActionMirrorFailed),
		ResourceType:       "audit_entry",
		ResourceID:         entry.ID,
		Result:             AuditResultFailure,
		FailureReason:      reason,
		RequestID:          entry.RequestID,
		CorrelationID:      entry.CorrelationID,
		MetadataJSON: marshalJSONObject(map[string]any{
			"audit_entry_id": entry.ID,
			"attempts":       attempts,
		}),
	}
	return m.store.CompleteAuditMirrorDelivery(ctx, params)
}

// settle records a final outcome other than giving up.
func (m *AuditMirror) settle(ctx context.Context, delivery AuditMirrorDelivery, status AuditMirrorDeliveryStatus, reason, messageID string) error {
	return m.store.CompleteAuditMirrorDelivery(ctx, CompleteAuditMirrorDeliveryParams{
		AuditEntryID:       delivery.Entry.ID,
		LeaseToken:         delivery.LeaseToken,
		Status:             status,
		Attempts:           delivery.Attempts,
		LastError:          reason,
		DeliveredMessageID: messageID,
	})
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
