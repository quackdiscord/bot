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
	UpsertGuild(context.Context, UpsertGuildParams) (*Guild, error)
	UpsertStaffMember(context.Context, UpsertStaffMemberParams) (*StaffMember, error)
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
	CountTemplateCasesForTarget(context.Context, CountTemplateCasesForTargetParams) (int64, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	CreateCase(context.Context, CreateCaseParams) (*CreatedCase, error)
	GetAppealByCaseID(ctx context.Context, caseID string) (*Appeal, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	// GetCaseByIDOrNumber resolves ref as a case ID or a guild case number.
	GetCaseByIDOrNumber(ctx context.Context, guildID, ref string) (*Case, error)
	GetCaseByIdempotencyKey(ctx context.Context, guildID, key string) (*Case, error)
	GetCaseNotification(ctx context.Context, caseID string) (*CaseNotification, error)
	GetCaseTemplateExpanded(ctx context.Context, guildID, templateID string) (*ExpandedCaseTemplate, error)
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	ListCaseActionAttempts(ctx context.Context, executionIDs []string) ([]CaseActionAttempt, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	ListCaseActionsForCases(ctx context.Context, caseIDs []string) ([]CaseActionExecution, error)
	ListCaseEvents(ctx context.Context, caseID string) ([]CaseEvent, error)
	ListCaseEvidence(ctx context.Context, caseID string) ([]CaseEvidenceSnapshot, []CaseEvidenceAttachment, error)
	ListCasesFiltered(context.Context, ListCasesParams) (*ListCasesResult, error)
	TargetCaseSummary(ctx context.Context, guildID, targetDiscordUserID string) (*TargetCaseSummary, error)
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
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	DismissCaseAction(context.Context, DismissCaseActionParams) (*CaseActionExecution, error)
	GetAppealByID(ctx context.Context, appealID string) (*Appeal, error)
	GetCaseActionExecution(ctx context.Context, guildID, executionID string) (*CaseActionExecution, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetCaseByIDOrNumber(ctx context.Context, guildID, ref string) (*Case, error)
	GetCaseNotification(ctx context.Context, caseID string) (*CaseNotification, error)
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	ListFailedCaseActions(context.Context, FailedCaseActionFilter) (*FailedCaseActionResult, error)
	PrepareCaseNotification(ctx context.Context, caseID, channelID, errorMessage string) error
	QueueCaseReversal(context.Context, QueueCaseReversalParams) (*CaseActionExecution, error)
	RetryCaseAction(context.Context, RetryCaseActionParams) (*CaseActionExecution, error)
}

// EvidenceStore is what EvidenceService needs from storage.
type EvidenceStore interface {
	GetGuildByDiscordID(ctx context.Context, discordGuildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	UpdateGuildSettings(context.Context, UpdateGuildSettingsParams) (*GuildSettings, error)
}

// AppealStore is what AppealService needs from storage.
type AppealStore interface {
	AppendAppealInformation(context.Context, AppendAppealInformationParams) (*Appeal, error)
	CreateAppeal(context.Context, CreateAppealParams) (*Appeal, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	GetAppealByCaseID(ctx context.Context, caseID string) (*Appeal, error)
	GetAppealByID(ctx context.Context, appealID string) (*Appeal, error)
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetGuildAppealSettings(ctx context.Context, guildID string) (*GuildAppealSettings, error)
	ListAppealEvents(ctx context.Context, appealID string) ([]AppealEvent, error)
	ListAppeals(context.Context, AppealListParams) (*AppealListResult, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	TransitionAppeal(context.Context, TransitionAppealParams) (*Appeal, error)
	UpdateGuildAppealSettings(context.Context, UpdateGuildAppealSettingsParams) (*GuildAppealSettings, error)
}

// AppealNotificationStore is the appeal outbox drained by
// AppealNotificationDispatcher.
type AppealNotificationStore interface {
	ClaimPendingAppealNotifications(ctx context.Context, limit int) ([]AppealNotification, error)
	CompleteAppealNotification(context.Context, CompleteAppealNotificationParams) error
}

// AuditStore is what AuditService needs from storage.
type AuditStore interface {
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	ListAuditLogEntriesFiltered(context.Context, ListAuditLogEntriesParams) (*ListAuditLogEntriesResult, error)
}

// AuditMirrorStore is what AuditMirrorWorker needs from storage.
type AuditMirrorStore interface {
	ClearGuildChannelReferences(ctx context.Context, guildID, channelID string, audit *AuditLogEntry) (*GuildSettings, error)
	CreateAuditLogEntry(context.Context, *AuditLogEntry) error
	GetGuildByID(ctx context.Context, guildID string) (*Guild, error)
	GetGuildSettings(ctx context.Context, guildID string) (*GuildSettings, error)
	// ListPendingAuditMirrorEntries returns important entries not yet
	// delivered or skipped. Failed deliveries come back after a minute.
	ListPendingAuditMirrorEntries(ctx context.Context, limit int) ([]AuditLogEntry, error)
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
	EvidenceStore
	AppealStore
	AuditStore
	StatisticsStore
	OpsStore
}

// ExpandedCaseTemplate is a template with its context fields, levels, and
// level actions.
type ExpandedCaseTemplate struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
}

// ExpandedCaseTemplateLevel is a level with its action.
type ExpandedCaseTemplateLevel struct {
	Level   CaseTemplateLevel
	Actions []CaseTemplateLevelAction
}

// CreateCaseTemplateParams creates a template at version 1.
type CreateCaseTemplateParams struct {
	Template      CaseTemplate
	ContextFields []CaseTemplateContextField
	Levels        []ExpandedCaseTemplateLevel
	Audit         *AuditLogEntry
}

// UpdateCaseTemplateParams replaces a template's policy and bumps its
// version.
type UpdateCaseTemplateParams struct {
	GuildID, TemplateID string
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
type FailedCaseActionResult struct {
	Executions []CaseActionExecution
	Total      int64
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

// UpdateGuildSettingsParams replaces a guild's settings.
type UpdateGuildSettingsParams struct {
	Settings GuildSettings
	Audit    *AuditLogEntry
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

// UpdateGuildAppealSettingsParams replaces a guild's appeal form.
type UpdateGuildAppealSettingsParams struct {
	Settings GuildAppealSettings
	Audit    AuditLogEntry
}

// CompleteAppealNotificationParams records a delivery outcome.
type CompleteAppealNotificationParams struct {
	NotificationID, LeaseToken, DeliveryMessageID, ErrorCode string
	Status                                                   AppealNotificationStatus
}
