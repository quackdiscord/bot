package quack

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
)

// AuditSource says which adapter or automation produced an audit entry.
type AuditSource string

// Audit sources.
const (
	AuditSourceAPI      AuditSource = "api"
	AuditSourceWeb      AuditSource = "web"
	AuditSourceDiscord  AuditSource = "discord"
	AuditSourceSystem   AuditSource = "system"
	AuditSourceImport   AuditSource = "import"
	AuditSourceHoneypot AuditSource = "honeypot"
)

func validAuditSource(source AuditSource) bool {
	switch source {
	case AuditSourceAPI, AuditSourceWeb, AuditSourceDiscord, AuditSourceSystem, AuditSourceImport, AuditSourceHoneypot:
		return true
	default:
		return false
	}
}

// AuditResult is the outcome an audit entry records.
type AuditResult string

// Audit results.
const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailure AuditResult = "failure"
	AuditResultDenied  AuditResult = "denied"
)

func validAuditResult(result AuditResult) bool {
	switch result {
	case AuditResultSuccess, AuditResultFailure, AuditResultDenied:
		return true
	default:
		return false
	}
}

// AuditAction is the stable, filterable name of an audited event.
type AuditAction string

// Audit actions written by the core. Modules write their own action names.
const (
	AuditActionAuthorizationDenied        AuditAction = "authorization.denied"
	AuditActionAuditRead                  AuditAction = "audit.read"
	AuditActionStatisticsRead             AuditAction = "statistics.read"
	AuditActionCaseCreate                 AuditAction = "case.create"
	AuditActionCaseRead                   AuditAction = "case.read"
	AuditActionCaseSearch                 AuditAction = "case.search"
	AuditActionCaseHistoryRead            AuditAction = "case.history.read"
	AuditActionCaseVoid                   AuditAction = "case.void"
	AuditActionEvidenceCapture            AuditAction = "evidence.capture"
	AuditActionTemplateCreate             AuditAction = "case_template.create"
	AuditActionTemplateUpdate             AuditAction = "case_template.update"
	AuditActionTemplateArchive            AuditAction = "case_template.archive"
	AuditActionTemplateRestore            AuditAction = "case_template.restore"
	AuditActionTemplateImport             AuditAction = "case_template.import"
	AuditActionTemplateExport             AuditAction = "case_template.export"
	AuditActionTemplateRead               AuditAction = "case_template.read"
	AuditActionSettingsRead               AuditAction = "guild_settings.read"
	AuditActionSettingsUpdate             AuditAction = "guild_settings.update"
	AuditActionActionAttempt              AuditAction = "case_action.attempt"
	AuditActionActionSucceeded            AuditAction = "case_action.succeeded"
	AuditActionActionRetrying             AuditAction = "case_action.retrying"
	AuditActionActionFailed               AuditAction = "case_action.failed"
	AuditActionActionRetry                AuditAction = "case_action.retry"
	AuditActionActionDismiss              AuditAction = "case_action.dismiss"
	AuditActionActionReverse              AuditAction = "case_action.reverse"
	AuditActionActionFailureRead          AuditAction = "case_action.failures.read"
	AuditActionActionRecovered            AuditAction = "case_action.recovered"
	AuditActionNotificationSent           AuditAction = "case_notification.sent"
	AuditActionNotificationFailed         AuditAction = "case_notification.failed"
	AuditActionAppealRead                 AuditAction = "appeal.read"
	AuditActionAppealSettingsUpdate       AuditAction = "appeal.settings.update"
	AuditActionAppealSubmit               AuditAction = "appeal.submit"
	AuditActionAppealInformationSubmit    AuditAction = "appeal.information.submit"
	AuditActionAppealQueueRead            AuditAction = "appeal.queue.read"
	AuditActionAppealInformationRequested AuditAction = "appeal.information_requested"
	AuditActionAppealReopened             AuditAction = "appeal.reopened"
	AuditActionAppealAccepted             AuditAction = "appeal.accepted"
	AuditActionAppealRejected             AuditAction = "appeal.rejected"
	AuditActionAppealClose                AuditAction = "appeal.close"
	AuditActionAppealClosed               AuditAction = "appeal.closed"
	AuditActionCaseVoidAppeal             AuditAction = "case.void.appeal"
	AuditActionMirrorDelivered            AuditAction = "audit_mirror.delivered"
	AuditActionMirrorFailed               AuditAction = "audit_mirror.failed"
	AuditActionMirrorRepaired             AuditAction = "audit_mirror.repaired"
	AuditActionMirrorSkipped              AuditAction = "audit_mirror.skipped"
	AuditActionImportBatch                AuditAction = "v4_import.batch"
	AuditActionHoneypotTrigger            AuditAction = "honeypot.trigger"
)

// importantAuditActions are mirrored to a guild's audit channel when one is
// configured. Reads and routine worker steps are left out to keep the
// channel readable.
var importantAuditActions = []AuditAction{
	AuditActionCaseCreate,
	AuditActionCaseVoid,
	AuditActionTemplateCreate,
	AuditActionTemplateUpdate,
	AuditActionTemplateArchive,
	AuditActionTemplateRestore,
	AuditActionTemplateImport,
	AuditActionSettingsUpdate,
	AuditActionActionSucceeded,
	AuditActionActionFailed,
	AuditActionActionRetry,
	AuditActionActionDismiss,
	AuditActionActionReverse,
	AuditActionActionRecovered,
	AuditActionNotificationFailed,
	AuditActionAppealSettingsUpdate,
	AuditActionAppealSubmit,
	AuditActionAppealInformationSubmit,
	AuditActionAppealInformationRequested,
	AuditActionAppealReopened,
	AuditActionAppealAccepted,
	AuditActionAppealRejected,
	AuditActionAppealClose,
	AuditActionAppealClosed,
	AuditActionCaseVoidAppeal,
	AuditActionImportBatch,
	AuditActionHoneypotTrigger,
	"guild.lifecycle.bootstrap",
	"guild.lifecycle.leave",
	"guild_settings.channel_reference.cleared",
	"guild_settings.channel_references.repaired",
	"case_template.bootstrap",
	"evidence_channel.ensure",
	"ticket.settings.update",
	"ticket.open",
	"ticket.resolve",
	"ticket.cancel",
	"ticket.reopen",
	"ticket.entry_channel_repair",
	"ticket.v4_import",
	"general_logging.settings.update",
	"general_logging.channel_repair",
	"general_logging.v4_settings_import",
	"honeypot.settings.update",
	"honeypot.trigger.failed",
	"honeypot.case.created",
	"honeypot.configuration.disabled",
	"honeypot.v4_settings_import",
}

// ImportantAuditActions returns, sorted, the audit actions the mirror worker
// sends to a guild's audit channel.
func ImportantAuditActions() []string {
	actions := make([]string, 0, len(importantAuditActions))
	for _, action := range importantAuditActions {
		actions = append(actions, string(action))
	}
	slices.Sort(actions)
	return actions
}

// AuditMetadataRedactedValue replaces sensitive values in stored audit
// metadata.
const AuditMetadataRedactedValue = "[REDACTED]"

// sensitiveAuditKeyFragments mark metadata keys whose values are never
// stored: credentials, raw payloads, and member-written content.
var sensitiveAuditKeyFragments = []string{
	"token", "secret", "password", "cookie", "authorization", "webhook", "session",
	"access_key", "private_key", "payload", "message_content", "member_content", "transcript",
}

// RedactAuditMetadata returns raw as a JSON object with sensitive values
// replaced at every depth. Anything that is not a JSON object is dropped
// entirely, since it cannot be inspected key by key.
func RedactAuditMetadata(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "{}"
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return `{"redaction":"invalid_metadata_removed"}`
	}
	object, ok := value.(map[string]any)
	if !ok {
		return `{"redaction":"non_object_metadata_removed"}`
	}
	redactAuditValue(object)
	body, err := json.Marshal(object)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func redactAuditValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveAuditKey(key) {
				typed[key] = AuditMetadataRedactedValue
				continue
			}
			redactAuditValue(child)
		}
	case []any:
		for _, child := range typed {
			redactAuditValue(child)
		}
	}
}

func sensitiveAuditKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"))
	for _, fragment := range sensitiveAuditKeyFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

// auditWriter is the one store method every audited service needs.
type auditWriter interface {
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
}

// recordAudit writes entry and logs when it can't. The log never includes
// the entry body or the driver error, which may contain member content.
func recordAudit(ctx context.Context, writer auditWriter, entry *AuditLogEntry) error {
	err := writer.CreateAuditLogEntry(ctx, entry)
	if err != nil {
		logger := slog.Default()
		if entry != nil {
			logger = logger.With("guild_id", entry.GuildID, "action", entry.Action, "resource_id", entry.ResourceID)
		}
		logger.ErrorContext(ctx, "Audit entry could not be recorded")
	}
	return err
}

// staffAudit builds an audit entry attributed to the staff member in
// guildContext. It returns nil when there is no staff member to attribute,
// which callers treat as "nothing to record".
func staffAudit(ctx context.Context, guildContext *GuildStaffContext, action, resourceType, resourceID string, result AuditResult, failureReason string) *AuditLogEntry {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	if resourceID == "" {
		resourceID = "unknown"
	}
	return &AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  guildContext.Staff.DiscordUserID,
		ActorPermissionBits: guildContext.PermissionBits,
		Source:              AuditSourceFromContext(ctx),
		Action:              action,
		ResourceType:        resourceType,
		ResourceID:          resourceID,
		Result:              result,
		FailureReason:       failureReason,
		CorrelationID:       correlationID,
		RequestID:           requestID,
		MetadataJSON:        "{}",
	}
}

// parseJSON decodes a stored JSON column for a response, returning an empty
// object when the column is empty or malformed.
func parseJSON(body string) any {
	if body == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return map[string]any{}
	}
	return value
}

// marshalJSONObject encodes metadata, falling back to an empty object.
func marshalJSONObject(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}
