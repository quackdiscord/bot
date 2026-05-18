package structs

import (
	"time"

	"gorm.io/gorm"
)

type GuildRolloutState string

const (
	GuildRolloutDisabled GuildRolloutState = "disabled"
	GuildRolloutBeta     GuildRolloutState = "beta"
	GuildRolloutEnabled  GuildRolloutState = "enabled"
)

type PermissionAction string

const (
	PermissionActionCaseCreate         PermissionAction = "case.create"
	PermissionActionCaseTemplateRead   PermissionAction = "case_template.read"
	PermissionActionCaseTemplateWrite  PermissionAction = "case_template.write"
	PermissionActionCaseTemplateDelete PermissionAction = "case_template.delete"
	PermissionActionAppealReview       PermissionAction = "appeal.review"
	PermissionActionTicketResolve      PermissionAction = "ticket.resolve"
	PermissionActionAuditRead          PermissionAction = "audit.read"
)

type CaseSeverity string

const (
	CaseSeverityLow      CaseSeverity = "low"
	CaseSeverityMedium   CaseSeverity = "medium"
	CaseSeverityHigh     CaseSeverity = "high"
	CaseSeverityCritical CaseSeverity = "critical"
)

type CaseStatus string

const (
	CaseStatusOpen          CaseStatus = "open"
	CaseStatusActionRunning CaseStatus = "action_running"
	CaseStatusCompleted     CaseStatus = "completed"
	CaseStatusFailed        CaseStatus = "failed"
	CaseStatusAppealed      CaseStatus = "appealed"
	CaseStatusVoided        CaseStatus = "voided"
)

type CaseSource string

const (
	CaseSourceDiscordCommand CaseSource = "discord_command"
	CaseSourceAPI            CaseSource = "api"
	CaseSourceAutomation     CaseSource = "automation"
	CaseSourceImport         CaseSource = "import"
)

type ActionType string

const (
	ActionRecordWarning ActionType = "record_warning"
	ActionSendDM        ActionType = "send_dm"
	ActionTimeoutUser   ActionType = "timeout_user"
	ActionKickUser      ActionType = "kick_user"
	ActionBanUser       ActionType = "ban_user"
	ActionWriteModLog   ActionType = "write_mod_log"
)

type ActionExecutionStatus string

const (
	ActionExecutionPending   ActionExecutionStatus = "pending"
	ActionExecutionRunning   ActionExecutionStatus = "running"
	ActionExecutionSucceeded ActionExecutionStatus = "succeeded"
	ActionExecutionFailed    ActionExecutionStatus = "failed"
	ActionExecutionRetrying  ActionExecutionStatus = "retrying"
	ActionExecutionSkipped   ActionExecutionStatus = "skipped"
	ActionExecutionCancelled ActionExecutionStatus = "cancelled"
)

type ActionAttemptStatus string

const (
	ActionAttemptSucceeded ActionAttemptStatus = "succeeded"
	ActionAttemptFailed    ActionAttemptStatus = "failed"
)

type EventVisibility string

const (
	EventVisibilityInternal EventVisibility = "internal"
	EventVisibilityStaff    EventVisibility = "staff"
	EventVisibilityPublic   EventVisibility = "public"
)

type CaseEventType string

const (
	CaseEventCreated         CaseEventType = "case_created"
	CaseEventNoteAdded       CaseEventType = "note_added"
	CaseEventNoteEdited      CaseEventType = "note_edited"
	CaseEventNoteDeleted     CaseEventType = "note_deleted"
	CaseEventActionQueued    CaseEventType = "action_queued"
	CaseEventActionSucceeded CaseEventType = "action_succeeded"
	CaseEventActionFailed    CaseEventType = "action_failed"
	CaseEventAppealCreated   CaseEventType = "appeal_created"
	CaseEventStatusChanged   CaseEventType = "status_changed"
)

type EscalationScope string

const (
	EscalationScopeUser  EscalationScope = "user"
	EscalationScopeGuild EscalationScope = "guild"
)

type AppealStatus string

const (
	AppealStatusPending  AppealStatus = "pending"
	AppealStatusAccepted AppealStatus = "accepted"
	AppealStatusRejected AppealStatus = "rejected"
	AppealStatusClosed   AppealStatus = "closed"
)

type TicketStatus string

const (
	TicketStatusOpen      TicketStatus = "open"
	TicketStatusResolved  TicketStatus = "resolved"
	TicketStatusCancelled TicketStatus = "cancelled"
)

type AuditSource string

const (
	AuditSourceAPI     AuditSource = "api"
	AuditSourceWeb     AuditSource = "web"
	AuditSourceDiscord AuditSource = "discord"
	AuditSourceSystem  AuditSource = "system"
)

type AuditResult string

const (
	AuditResultSuccess AuditResult = "success"
	AuditResultFailure AuditResult = "failure"
)

type ULIDModel struct {
	ID        string    `gorm:"type:char(26);primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

type Guild struct {
	ULIDModel
	DiscordGuildID     string            `gorm:"size:32;not null;uniqueIndex"`
	Name               string            `gorm:"size:191;not null"`
	IconURL            string            `gorm:"size:1024"`
	OwnerDiscordUserID string            `gorm:"size:32;not null;index"`
	RolloutState       GuildRolloutState `gorm:"size:32;not null;default:'disabled';index"`
	IsActive           bool              `gorm:"not null;default:true;index"`
	ImportedFromV4     bool              `gorm:"not null;default:false"`
	ImportedAt         *time.Time
}

type GuildSettings struct {
	ULIDModel
	GuildID                       string `gorm:"type:char(26);not null;uniqueIndex"`
	UseDiscordPermissionChecks    bool   `gorm:"not null;default:true"`
	DefaultTemplatePermissionBits uint64 `gorm:"type:bigint unsigned;not null;default:0"`
	ModLogChannelDiscordID        string `gorm:"size:32"`
	AppealsChannelDiscordID       string `gorm:"size:32"`
	TicketChannelDiscordID        string `gorm:"size:32"`
	TicketLogChannelDiscordID     string `gorm:"size:32"`
	HoneypotChannelDiscordID      string `gorm:"size:32"`
	FeatureFlagsJSON              string `gorm:"type:json;not null"`
	GuildModerationConfigJSON     string `gorm:"type:json;not null"`
}

type GuildPermissionPolicy struct {
	ULIDModel
	GuildID               string           `gorm:"type:char(26);not null;uniqueIndex:idx_guild_permission_policy,priority:1"`
	Action                PermissionAction `gorm:"size:96;not null;uniqueIndex:idx_guild_permission_policy,priority:2"`
	MinimumPermissionBits uint64           `gorm:"type:bigint unsigned;not null;default:0"`
	Description           string           `gorm:"size:255"`
}

type StaffMember struct {
	ULIDModel
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_staff_member_guild_user,priority:1;index"`
	DiscordUserID          string     `gorm:"size:32;not null;uniqueIndex:idx_staff_member_guild_user,priority:2;index"`
	LastSeenPermissionBits uint64     `gorm:"type:bigint unsigned;not null;default:0"`
	LastKnownDisplayName   string     `gorm:"size:191"`
	LastActiveAt           *time.Time `gorm:"index"`
	DisabledAt             *time.Time
}

type CaseTemplate struct {
	ULIDModel
	GuildID                string         `gorm:"type:char(26);not null;uniqueIndex:idx_case_template_guild_slug,priority:1;index:idx_case_template_guild_enabled,priority:1"`
	Slug                   string         `gorm:"size:64;not null;uniqueIndex:idx_case_template_guild_slug,priority:2"`
	Name                   string         `gorm:"size:191;not null"`
	Description            string         `gorm:"type:text;not null"`
	ReasonTemplate         string         `gorm:"type:text;not null"`
	RequiredPermissionBits uint64         `gorm:"type:bigint unsigned;not null;default:0"`
	DefaultSeverity        CaseSeverity   `gorm:"size:32;not null;default:'medium'"`
	DefaultWeight          int            `gorm:"not null;default:1"`
	DMEnabled              bool           `gorm:"not null;default:false"`
	DMTemplate             string         `gorm:"type:text"`
	Enabled                bool           `gorm:"not null;default:true;index:idx_case_template_guild_enabled,priority:2"`
	Version                uint           `gorm:"not null;default:1"`
	CreatedByDiscordUserID string         `gorm:"size:32;not null"`
	UpdatedByDiscordUserID string         `gorm:"size:32;not null"`
	ArchivedAt             *time.Time     `gorm:"index"`
	DeletedAt              gorm.DeletedAt `gorm:"index"`
}

type CaseTemplateAction struct {
	ULIDModel
	TemplateID             string     `gorm:"type:char(26);not null;uniqueIndex:idx_template_action_position,priority:1;index"`
	Position               int        `gorm:"not null;uniqueIndex:idx_template_action_position,priority:2"`
	ActionType             ActionType `gorm:"size:64;not null;index"`
	RequiredPermissionBits uint64     `gorm:"type:bigint unsigned;not null;default:0"`
	ConfigJSON             string     `gorm:"type:json;not null"`
	ContinueOnError        bool       `gorm:"not null;default:false"`
	MaxRetries             uint8      `gorm:"not null;default:0"`
	RetryBackoffMS         int        `gorm:"not null;default:0"`
	TimeoutMS              int        `gorm:"not null;default:0"`
	IdempotencyScope       string     `gorm:"size:32;not null;default:'case'"`
	Enabled                bool       `gorm:"not null;default:true"`
}

type CaseTemplateEscalationRule struct {
	ULIDModel
	GuildID              string          `gorm:"type:char(26);not null;index"`
	TemplateID           string          `gorm:"type:char(26);not null;index:idx_escalation_template_priority,priority:1"`
	Name                 string          `gorm:"size:191;not null"`
	Scope                EscalationScope `gorm:"size:32;not null;default:'user'"`
	Priority             int             `gorm:"not null;default:0;index:idx_escalation_template_priority,priority:2"`
	TriggerCaseCount     int             `gorm:"not null;default:0"`
	TriggerWeightTotal   int             `gorm:"not null;default:0"`
	WindowMinutes        int             `gorm:"not null;default:0"`
	EscalateToTemplateID *string         `gorm:"type:char(26);index"`
	RuleConfigJSON       string          `gorm:"type:json;not null"`
	Enabled              bool            `gorm:"not null;default:true"`
	StopAfterMatch       bool            `gorm:"not null;default:true"`
}

type Case struct {
	ULIDModel
	GuildID                 string       `gorm:"type:char(26);not null;index:idx_case_guild_case_number,priority:1,unique;index:idx_case_guild_target,priority:1;index:idx_case_guild_mod,priority:1;index:idx_case_guild_status,priority:1"`
	CaseNumber              uint64       `gorm:"type:bigint unsigned;not null;index:idx_case_guild_case_number,priority:2,unique"`
	TemplateID              *string      `gorm:"type:char(26);index"`
	TemplateVersion         uint         `gorm:"not null;default:1"`
	TemplateSnapshotJSON    string       `gorm:"type:json;not null"`
	TargetDiscordUserID     string       `gorm:"size:32;not null;index:idx_case_guild_target,priority:2"`
	ModeratorDiscordUserID  string       `gorm:"size:32;not null;index:idx_case_guild_mod,priority:2"`
	Reason                  string       `gorm:"type:text;not null"`
	Severity                CaseSeverity `gorm:"size:32;not null;default:'medium'"`
	Weight                  int          `gorm:"not null;default:1"`
	Status                  CaseStatus   `gorm:"size:32;not null;default:'open';index:idx_case_guild_status,priority:2"`
	Source                  CaseSource   `gorm:"size:32;not null;default:'discord_command';index"`
	CorrelationID           string       `gorm:"size:128;index"`
	ContextChannelDiscordID string       `gorm:"size:32"`
	ContextMessageDiscordID string       `gorm:"size:32"`
	ContextURL              string       `gorm:"size:1024"`
	ResolvedAt              *time.Time   `gorm:"index"`
	ResolvedByDiscordUserID string       `gorm:"size:32"`
	MetadataJSON            string       `gorm:"type:json;not null"`
}

type CaseActionExecution struct {
	ULIDModel
	CaseID             string                `gorm:"type:char(26);not null;index:idx_action_execution_case_position,priority:1;index"`
	TemplateActionID   *string               `gorm:"type:char(26);index"`
	Position           int                   `gorm:"not null;index:idx_action_execution_case_position,priority:2"`
	ActionType         ActionType            `gorm:"size:64;not null;index"`
	Status             ActionExecutionStatus `gorm:"size:32;not null;default:'pending';index:idx_action_execution_status_retry,priority:1"`
	IdempotencyKey     string                `gorm:"size:191;not null;uniqueIndex"`
	ConfigSnapshotJSON string                `gorm:"type:json;not null"`
	AttemptCount       uint8                 `gorm:"not null;default:0"`
	MaxRetries         uint8                 `gorm:"not null;default:0"`
	RetryBackoffMS     int                   `gorm:"not null;default:0"`
	SafeForRetry       bool                  `gorm:"not null;default:true"`
	Irreversible       bool                  `gorm:"not null;default:false"`
	LastErrorCode      string                `gorm:"size:64"`
	LastError          string                `gorm:"type:text"`
	StartedAt          *time.Time
	FinishedAt         *time.Time
	NextRetryAt        *time.Time `gorm:"index:idx_action_execution_status_retry,priority:2"`
	CorrelationID      string     `gorm:"size:128;index"`
}

type CaseActionAttempt struct {
	ULIDModel
	ExecutionID         string              `gorm:"type:char(26);not null;uniqueIndex:idx_action_attempt_execution_number,priority:1;index"`
	AttemptNumber       uint8               `gorm:"not null;uniqueIndex:idx_action_attempt_execution_number,priority:2"`
	Status              ActionAttemptStatus `gorm:"size:32;not null;index"`
	WorkerID            string              `gorm:"size:64"`
	StartedAt           time.Time           `gorm:"not null"`
	FinishedAt          *time.Time
	DurationMS          int64  `gorm:"not null;default:0"`
	ErrorCode           string `gorm:"size:64"`
	ErrorMessage        string `gorm:"type:text"`
	RequestPayloadJSON  string `gorm:"type:json;not null"`
	ResponsePayloadJSON string `gorm:"type:json;not null"`
}

type CaseEvent struct {
	ULIDModel
	CaseID             string          `gorm:"type:char(26);not null;index:idx_case_event_case_created,priority:1"`
	GuildID            string          `gorm:"type:char(26);not null;index"`
	EventType          CaseEventType   `gorm:"size:64;not null;index"`
	ActorDiscordUserID string          `gorm:"size:32;index"`
	ActorType          string          `gorm:"size:32;not null;default:'system'"`
	Visibility         EventVisibility `gorm:"size:32;not null;default:'staff'"`
	Body               string          `gorm:"type:text;not null"`
	MetadataJSON       string          `gorm:"type:json;not null"`
	EditedAt           *time.Time
	DeletedAt          gorm.DeletedAt `gorm:"index"`
}

type Appeal struct {
	ULIDModel
	GuildID                 string       `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string      `gorm:"type:char(26);index"`
	TargetDiscordUserID     string       `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  AppealStatus `gorm:"size:32;not null;default:'pending';index:idx_appeal_guild_status,priority:2"`
	Content                 string       `gorm:"type:text;not null"`
	DecisionReason          string       `gorm:"type:text"`
	ReviewedByDiscordUserID string       `gorm:"size:32"`
	ReviewedAt              *time.Time   `gorm:"index"`
	ReviewMessageDiscordID  string       `gorm:"size:32"`
	MetadataJSON            string       `gorm:"type:json;not null"`
}

type AppealEvent struct {
	ULIDModel
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

type Ticket struct {
	ULIDModel
	GuildID                 string       `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string       `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string       `gorm:"size:32;uniqueIndex"`
	Status                  TicketStatus `gorm:"size:32;not null;default:'open';index:idx_ticket_guild_status,priority:2"`
	LogMessageDiscordID     string       `gorm:"size:32"`
	ResolvedByDiscordUserID string       `gorm:"size:32"`
	ResolvedAt              *time.Time   `gorm:"index"`
	TranscriptURL           string       `gorm:"size:1024"`
	MetadataJSON            string       `gorm:"type:json;not null"`
}

type TicketEvent struct {
	ULIDModel
	TicketID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

type AuditLogEntry struct {
	ULIDModel
	GuildID             string      `gorm:"type:char(26);not null;index:idx_audit_guild_action,priority:1;index"`
	ActorDiscordUserID  string      `gorm:"size:32;index"`
	ActorPermissionBits uint64      `gorm:"type:bigint unsigned;not null;default:0"`
	Source              AuditSource `gorm:"size:32;not null;index"`
	Action              string      `gorm:"size:96;not null;index:idx_audit_guild_action,priority:2"`
	ResourceType        string      `gorm:"size:64;not null;index:idx_audit_resource,priority:1"`
	ResourceID          string      `gorm:"size:64;not null;index:idx_audit_resource,priority:2"`
	Result              AuditResult `gorm:"size:32;not null;index"`
	FailureReason       string      `gorm:"type:text"`
	CorrelationID       string      `gorm:"size:128;index"`
	RequestID           string      `gorm:"size:128;index"`
	MetadataJSON        string      `gorm:"type:json;not null"`
}

func SchemaModels() []any {
	return []any{
		&Guild{},
		&GuildSettings{},
		&GuildPermissionPolicy{},
		&StaffMember{},
		&CaseTemplate{},
		&CaseTemplateAction{},
		&CaseTemplateEscalationRule{},
		&Case{},
		&CaseActionExecution{},
		&CaseActionAttempt{},
		&CaseEvent{},
		&Appeal{},
		&AppealEvent{},
		&Ticket{},
		&TicketEvent{},
		&AuditLogEntry{},
	}
}
