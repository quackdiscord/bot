package quack

import (
	"context"
	"time"
)

// Each service declares the store methods it calls. The store package
// implements all of them on one type; Store below is their union, used only
// to wire everything up in New.

// GuildStore is what GuildService needs from storage.
type GuildStore interface {
	BootstrapGuild(context.Context, BootstrapGuildParams) (*BootstrapGuildResult, error)
	ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *AuditLogEntry) (*GuildSettings, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	DeactivateGuild(ctx context.Context, discordGuildID string, audit *AuditLogEntry) (*Guild, error)
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	GetStaffMember(ctx context.Context, guildID, discordUserID string) (*StaffMember, error)
	// ListGuildStaffRoles returns the configured staff roles of the guilds
	// among discordGuildIDs that have settings, keyed by Discord guild ID,
	// in one query.
	ListGuildStaffRoles(ctx context.Context, discordGuildIDs []string) (map[string]StaffRoles, error)
	UpsertGuild(context.Context, UpsertGuildParams) (*Guild, error)
	UpsertStaffMember(context.Context, UpsertStaffMemberParams) (*StaffMember, error)
	MFAStatusReader
}

// SettingsStore is what GuildSettingsService needs from storage.
type SettingsStore interface {
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	UpdateGuildSettings(context.Context, UpdateGuildSettingsParams) (*GuildSettings, error)
}

// TemplateStore is what TemplateService needs from storage.
type TemplateStore interface {
	ArchiveCaseTemplate(ctx context.Context, guildID, templateID string, audit *AuditLogEntry) (*ExpandedCaseTemplate, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	CreateCaseTemplate(context.Context, CreateCaseTemplateParams) (*ExpandedCaseTemplate, error)
	GetCaseTemplateBySlug(ctx context.Context, guildID, slug string) (*CaseTemplate, error)
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*ExpandedCaseTemplate, error)
	ListCaseTemplates(ctx context.Context, guildID string) ([]ExpandedCaseTemplate, error)
	RestoreCaseTemplate(ctx context.Context, guildID, templateID string, audit *AuditLogEntry) (*ExpandedCaseTemplate, error)
	UpdateCaseTemplate(context.Context, UpdateCaseTemplateParams) (*ExpandedCaseTemplate, error)
}

// CaseStore is what CaseService needs from storage.
type CaseStore interface {
	caseReceiptStore
	// AppendCaseEvidence adds evidence to an existing case with its audit
	// entry, and requests a refresh of the case's publications.
	AppendCaseEvidence(context.Context, AppendCaseEvidenceParams) error
	CountTemplateCasesForTarget(context.Context, CountTemplateCasesForTargetParams) (int64, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	CreateCase(context.Context, CreateCaseParams) (*CreatedCase, error)
	GetAppealByCaseID(ctx context.Context, caseID string) (*Appeal, error)
	// GetCaseByIDOrNumber resolves ref as a case ID or a guild case number.
	GetCaseByIDOrNumber(ctx context.Context, guildID, ref string) (*Case, error)
	GetCaseByIdempotencyKey(ctx context.Context, guildID, key string) (*Case, error)
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*ExpandedCaseTemplate, error)
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	// GetCaseEvidencePage returns the case's evidence item at 1-based
	// position, oldest first and clamped into range, with its attachments
	// and the case's evidence count. The item is nil when there is none.
	GetCaseEvidencePage(ctx context.Context, caseID string, position int) (*CaseEvidenceSnapshot, []CaseEvidenceAttachment, int64, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	ListCaseActionsForCases(ctx context.Context, caseIDs []string) ([]CaseActionExecution, error)
	ListCaseEvents(ctx context.Context, caseID string) ([]CaseEvent, error)
	ListCaseEvidence(ctx context.Context, caseID string) ([]CaseEvidenceSnapshot, []CaseEvidenceAttachment, error)
	ListCasesFiltered(context.Context, ListCasesParams) (*ListCasesResult, error)
	// ListRecentCaseEvents returns the case's latest limit (1 to 100) events,
	// oldest first.
	ListRecentCaseEvents(ctx context.Context, caseID string, limit int) ([]CaseEvent, error)
	TargetCaseSummary(ctx context.Context, guildID, targetDiscordUserID string) (*TargetCaseSummary, error)
	// UpdateCaseContext replaces a case's context values with its audit
	// entry, and requests a refresh of the case's publications. It returns
	// nil when the case is not in the guild.
	UpdateCaseContext(context.Context, UpdateCaseContextParams) (*Case, error)
	VoidCase(context.Context, VoidCaseParams) (*Case, error)
	// WithGuildCaseLock runs fn in a transaction holding the guild's case
	// lock, which serializes case numbering and escalation counts. fn must
	// use the CaseStore it is given.
	WithGuildCaseLock(ctx context.Context, guildID string, fn func(CaseStore) error) error
}

// ActionStore is what ActionService needs from storage.
type ActionStore interface {
	BeginCaseNotificationDelivery(ctx context.Context, notificationID, leaseToken string) error
	ClaimCaseNotification(context.Context, ClaimCaseNotificationParams) (*CaseNotification, error)
	// ClaimNextCaseAction leases the case's next due execution, or returns
	// nil when nothing is due. An expired lease is failed for staff review.
	ClaimNextCaseAction(context.Context, ClaimCaseActionParams) (*ClaimedCaseAction, error)
	CompleteCaseAction(context.Context, CompleteCaseActionParams) error
	CompleteCaseNotification(context.Context, CompleteCaseNotificationParams) error
	// CompetingPunishmentExists reports whether another execution of the
	// same kind against the same member may still be in effect: one that
	// is queued or running, or that succeeded or failed after the original
	// started. Reversing the original could then undo that punishment too.
	CompetingPunishmentExists(ctx context.Context, guildID, caseID, originalExecutionID string) (bool, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	DismissCaseAction(context.Context, DismissCaseActionParams) (*CaseActionExecution, error)
	GetAppealByID(ctx context.Context, appealID string) (*Appeal, error)
	GetCaseActionExecution(ctx context.Context, guildID, executionID string) (*CaseActionExecution, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetCaseByIDOrNumber(ctx context.Context, guildID, ref string) (*Case, error)
	GetCaseNotification(ctx context.Context, caseID string) (*CaseNotification, error)
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	ListCaseActionAttempts(ctx context.Context, executionIDs []string) ([]CaseActionAttempt, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	ListFailedCaseActions(context.Context, FailedCaseActionFilter) (*FailedCaseActionResult, error)
	PrepareCaseNotification(ctx context.Context, caseID, channelID, errorMessage string) error
	QueueCaseReversal(context.Context, QueueCaseReversalParams) (*CaseActionExecution, error)
	RetryCaseAction(context.Context, RetryCaseActionParams) (*CaseActionExecution, error)
}

// AppealStore is what AppealService needs from storage.
type AppealStore interface {
	AppendAppealInformation(context.Context, AppendAppealInformationParams) (*Appeal, error)
	CreateAppeal(context.Context, CreateAppealParams) (*Appeal, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	GetAppealByCaseID(ctx context.Context, caseID string) (*Appeal, error)
	GetAppealByID(ctx context.Context, appealID string) (*Appeal, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	ListAppealEvents(ctx context.Context, appealID string) ([]AppealEvent, error)
	ListAppeals(context.Context, AppealListParams) (*AppealListResult, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	TransitionAppeal(context.Context, TransitionAppealParams) (*Appeal, error)
}

// AppealNotificationStore is the appeal outbox drained by
// AppealNotificationDispatcher, plus the appeal reads it needs to publish
// staff queue posts.
type AppealNotificationStore interface {
	AppealStore
	// BeginAppealNotificationDelivery moves a claimed row whose lease is
	// still held to sending, just before it goes to Discord.
	BeginAppealNotificationDelivery(ctx context.Context, notificationID, leaseToken string) error
	// ClaimPendingAppealNotifications leases up to limit rows that are
	// pending, claimed with an expired lease, or deferred over a minute ago.
	// Rows left sending past their lease fail as delivery_outcome_unknown
	// and are never sent again.
	ClaimPendingAppealNotifications(ctx context.Context, limit int) ([]AppealNotification, error)
	// CompleteAppealNotification records the outcome of a sending row. A
	// staff row that was asked to refresh meanwhile goes back to pending.
	CompleteAppealNotification(context.Context, CompleteAppealNotificationParams) error
}

// AuditStore is what AuditService needs from storage.
type AuditStore interface {
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	ListAuditLogEntriesFiltered(context.Context, ListAuditLogEntriesParams) (*ListAuditLogEntriesResult, error)
}

// AuditMirrorStore is what AuditMirror needs from storage.
type AuditMirrorStore interface {
	ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *AuditLogEntry) (*GuildSettings, error)
	GetAppealByID(ctx context.Context, appealID string) (*Appeal, error)
	GetCaseActionExecution(ctx context.Context, guildID, executionID string) (*CaseActionExecution, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	// ClaimAuditMirrorDeliveries leases up to limit due deliveries, oldest
	// first but fair across guilds, with their audit entries. A claim whose
	// lease lapsed before sending began is due again. A delivery left
	// sending past its lease fails as delivery_outcome_unknown and is never
	// sent again, so a crash cannot post an entry twice.
	ClaimAuditMirrorDeliveries(ctx context.Context, limit int) ([]AuditMirrorDelivery, error)
	// BeginAuditMirrorDelivery moves a claimed delivery to sending just
	// before it goes to Discord, provided the caller's lease is still live.
	// It returns ErrAuditMirrorLeaseLost otherwise.
	BeginAuditMirrorDelivery(ctx context.Context, auditEntryID, leaseToken string) error
	// CompleteAuditMirrorDelivery records the outcome of a claimed or
	// sending delivery the caller holds the lease for.
	CompleteAuditMirrorDelivery(context.Context, CompleteAuditMirrorDeliveryParams) error
}

// StatisticsStore is what StaffStatisticsService needs from storage.
type StatisticsStore interface {
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	DeriveStaffStatistics(context.Context, StaffStatisticsParams) (*StaffStatistics, error)
}

// OpsStore is what OpsService needs from storage.
type OpsStore interface {
	// ActionQueueSnapshot summarizes executions, for one guild or for all
	// guilds when guildID is empty.
	ActionQueueSnapshot(ctx context.Context, guildID string, failureLimit int) (*ActionQueueSnapshot, error)
}

// Store is every store method the services need.
type Store interface {
	GuildStore
	SettingsStore
	TemplateStore
	CaseStore
	ActionStore
	AppealStore
	AuditStore
	AuditMirrorStore
	StatisticsStore
	OpsStore
	AppealNotificationStore
	CasePublicationStore
}

// CreateCaseTemplateParams creates a template at version 1.
type CreateCaseTemplateParams struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
	Audit         *AuditLogEntry
}

// UpdateCaseTemplateParams replaces a template's policy and bumps its
// version. The store rejects the update with ErrTemplateConflict unless the
// template is still at ExpectedVersion.
type UpdateCaseTemplateParams struct {
	GuildID, TemplateID string
	ExpectedVersion     uint
	Template            CaseTemplate
	ContextFields       []CaseTemplateContextField
	Levels              []ExpandedCaseTemplateLevel
	Audit               *AuditLogEntry
}

// ListAuditLogEntriesParams filters the audit log. Empty fields match
// everything.
type ListAuditLogEntriesParams struct {
	GuildID, ActorDiscordUserID, Source, Action, ResourceType, ResourceID string
	CaseID, MemberDiscordUserID, CreatedAfter, CreatedBefore, BeforeID    string
	Result                                                                AuditResult
	Limit, Offset                                                         int
}

// ListAuditLogEntriesResult is a page of audit entries and the total match
// count.
type ListAuditLogEntriesResult struct {
	Entries []AuditLogEntry
	Total   int64
}

// CreateCaseParams is everything written when a case is created. The store
// assigns the case number and links the children to the new case.
type CreateCaseParams struct {
	Case             Case
	Event            CaseEvent
	ActionExecutions []CaseActionExecution
	Evidence         []CaseEvidenceSnapshot
	Attachments      []CaseEvidenceAttachment
	Notification     *CaseNotification
	Audit            *AuditLogEntry
	AdditionalAudits []AuditLogEntry
}

// CreatedCase is a newly committed case and its children.
type CreatedCase struct {
	Case             Case
	Event            CaseEvent
	ActionExecutions []CaseActionExecution
	Evidence         []CaseEvidenceSnapshot
	Attachments      []CaseEvidenceAttachment
	Notification     *CaseNotification
}

// CountTemplateCasesForTargetParams counts a member's valid cases under one
// template, excluding v4 imports. Escalation keys on this count.
type CountTemplateCasesForTargetParams struct {
	GuildID, TemplateID, TargetDiscordUserID string
	// CreatedAtOrAfter, when set, counts only cases created at or after it:
	// the start of the template's decay window.
	CreatedAtOrAfter *time.Time
}

// ListCasesParams filters a guild's cases. Empty fields match everything.
type ListCasesParams struct {
	GuildID, TargetDiscordUserID, ModeratorDiscordUserID, TemplateID    string
	CaseNumber, ActionResult, AppealStatus, CreatedAfter, CreatedBefore string
	Validity                                                            CaseValidity
	Limit, Offset                                                       int
}

// ListCasesResult is a page of cases and the total match count.
type ListCasesResult struct {
	Cases []Case
	Total int64
}

// TargetCaseSummary counts all of a member's cases in a guild.
type TargetCaseSummary struct {
	Total      int64
	ByValidity map[CaseValidity]int64
	ByTemplate map[string]int64
}

// ClaimedCaseAction is a leased execution and its case.
type ClaimedCaseAction struct {
	Case      Case
	Execution CaseActionExecution
}

// ClaimCaseActionParams identifies the case to claim work from and the
// worker claiming it.
type ClaimCaseActionParams struct{ CaseID, WorkerID string }

// CompleteCaseActionParams records the outcome of a leased attempt. The store
// rejects it if LeaseToken no longer holds the lease.
type CompleteCaseActionParams struct {
	ExecutionID                                                      string
	LeaseToken                                                       string
	AttemptNumber                                                    uint8
	WorkerID                                                         string
	AttemptStatus                                                    ActionAttemptStatus
	ExecutionStatus                                                  ActionExecutionStatus
	ErrorCode, ErrorMessage, RequestPayloadJSON, ResponsePayloadJSON string
	NextRetryAt                                                      *time.Time
	EventType                                                        CaseEventType
	EventBody, EventMetadataJSON, CorrelationID, RequestID           string
}

// VoidCaseParams voids a case and records why.
type VoidCaseParams struct {
	GuildID, CaseID, ActorDiscordUserID, Reason string
	ReplacementCaseID                           *string
	Audit                                       *AuditLogEntry
}

// RetryCaseActionParams requeues a failed execution after staff confirmed
// it is safe.
type RetryCaseActionParams struct {
	GuildID, ExecutionID, ActorDiscordUserID string
	Audit                                    *AuditLogEntry
}

// DismissCaseActionParams removes a failed execution from the review queue.
type DismissCaseActionParams struct {
	GuildID, ExecutionID, ActorDiscordUserID string
	Audit                                    *AuditLogEntry
}

// QueueCaseReversalParams queues a reversal of a succeeded execution. The
// service has already checked that the reversal is allowed.
type QueueCaseReversalParams struct {
	GuildID, CaseID, ActorDiscordUserID string
	OriginalExecutionID                 string
	AppealID                            *string
	ActionType                          ActionType
	Audit                               *AuditLogEntry
}

// FailedCaseActionFilter pages through a guild's failed executions.
type FailedCaseActionFilter struct {
	GuildID       string
	Limit, Offset int
}

// FailedCaseActionResult is a page of failed executions and the total.
// Cases describes the case behind each execution, keyed by case ID, so a
// review queue can name the case and member without a lookup per row.
type FailedCaseActionResult struct {
	Executions []CaseActionExecution
	Cases      map[string]FailedActionCase
	Total      int64
}

// FailedActionCase is what the failure queue shows about the case a failed
// execution belongs to.
type FailedActionCase struct {
	CaseNumber          uint64
	TargetDiscordUserID string
}

// ClaimCaseNotificationParams identifies the case whose notification a
// worker wants to send.
type ClaimCaseNotificationParams struct{ CaseID, WorkerID string }

// CompleteCaseNotificationParams records the outcome of a leased
// notification.
type CompleteCaseNotificationParams struct {
	NotificationID, LeaseToken, WorkerID                                string
	Status                                                              NotificationStatus
	PreparedChannelDiscordID, RenderedMessage, DeliveryMessageDiscordID string
	ErrorCode, ErrorMessage                                             string
	EventType                                                           CaseEventType
}

// UpsertGuildParams is the Discord metadata refreshed on each staff request.
type UpsertGuildParams struct{ DiscordGuildID, Name, IconURL, OwnerDiscordUserID string }

// BootstrapGuildParams installs or reactivates a guild.
type BootstrapGuildParams struct {
	DiscordGuildID, Name, IconURL, OwnerDiscordUserID string
	// KnownChannelDiscordIDs, when non-nil, is the guild's full channel list.
	// Configured channels missing from it are cleared.
	KnownChannelDiscordIDs []string
	// Starter is the template to create when the guild has no starter
	// template yet. The store saves it as given.
	Starter ExpandedCaseTemplate
}

// BootstrapGuildResult is the guild after bootstrap and whether anything was
// created.
type BootstrapGuildResult struct {
	Guild                  Guild
	Settings               GuildSettings
	StarterTemplate        ExpandedCaseTemplate
	GuildCreated           bool
	StarterTemplateCreated bool
}

// UpdateGuildSettingsParams changes some of a guild's settings, with the
// audit entry written in the same transaction. Only the fields Patch sets
// are written, so concurrent updates to other fields are never undone.
type UpdateGuildSettingsParams struct {
	GuildID string
	Patch   GuildSettingsPatch
	// ExpectedStaffRoles, when set, is the staff roles the update was judged
	// by. If the stored staff roles differ when the row is locked, the store
	// writes nothing and returns ErrGuildSettingsConflict, so a decision made
	// against roles that have since changed is never saved.
	ExpectedStaffRoles *StaffRoles
	Audit              *AuditLogEntry
}

// GuildSettingsPatch is a partial settings write. Nil fields leave the stored
// value alone; values are stored as given, already validated.
type GuildSettingsPatch struct {
	AppealQueueChannelDiscordID     *string
	AppealRejoinURL                 *string
	AppealReviewReasonRequired      *bool
	AuditMirrorChannelDiscordID     *string
	ManagedEvidenceChannelDiscordID *string
	NotificationIntroduction        *string
	NotificationFooter              *string
	ModeratorRoleIDs                *[]string
	RulesManagerRoleIDs             *[]string
	// StarterPolicyNoticeAcknowledgedAt, when set, acknowledges a pending
	// starter template notice at that time. An acknowledged notice keeps its
	// first acknowledgement.
	StarterPolicyNoticeAcknowledgedAt *time.Time
}

// UpsertStaffMemberParams refreshes a staff member's cached attribution.
type UpsertStaffMemberParams struct {
	GuildID, DiscordUserID string
	LastSeenPermissionBits uint64
	LastKnownDisplayName   string
	LastActiveAt           time.Time
}

// CreateAppealParams creates an appeal together with its first events,
// audit entry, and staff notification.
type CreateAppealParams struct {
	Appeal       Appeal
	Event        AppealEvent
	CaseEvent    CaseEvent
	Audit        AuditLogEntry
	Notification AppealNotification
}

// AppendAppealInformationParams adds a member's answer to a request for more
// information and returns the appeal to pending.
type AppendAppealInformationParams struct {
	AppealID, TargetDiscordUserID, Body string
	Event                               AppealEvent
	Audit                               AuditLogEntry
	Notification                        AppealNotification
}

// TransitionAppealParams moves an appeal from one of AllowedFrom to To, and
// voids the case in the same transaction when VoidCase is set.
type TransitionAppealParams struct {
	GuildID, AppealID, ActorDiscordUserID string
	AllowedFrom                           []AppealStatus
	To                                    AppealStatus
	Reason                                string
	Event                                 AppealEvent
	AppealAudit                           AuditLogEntry
	CaseAudit                             *AuditLogEntry
	Notification                          AppealNotification
	VoidCase                              bool
}

// AppealListParams pages through a guild's appeals.
type AppealListParams struct {
	GuildID       string
	Status        AppealStatus
	Limit, Offset int
}

// AppealListResult is a page of appeals and the total.
type AppealListResult struct {
	Appeals []Appeal
	Total   int64
}

// CompleteAppealNotificationParams records a delivery outcome. Empty
// delivery fields keep what the row already had.
type CompleteAppealNotificationParams struct {
	NotificationID, LeaseToken           string
	DeliveryChannelID, DeliveryMessageID string
	ErrorCode                            string
	Status                               AppealNotificationStatus
}
