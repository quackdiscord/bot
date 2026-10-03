package quack

import "time"

// PermissionAction names a capability a staff member can hold in a guild.
// Capabilities are derived from live Discord permissions, never stored.
type PermissionAction string

// The capabilities checked by the services and HTTP middleware.
const (
	PermissionActionCaseCreate         PermissionAction = "case.create"
	PermissionActionCaseRead           PermissionAction = "case.read"
	PermissionActionCaseTemplateRead   PermissionAction = "case_template.read"
	PermissionActionCaseTemplateWrite  PermissionAction = "case_template.write"
	PermissionActionCaseTemplateDelete PermissionAction = "case_template.delete"
	PermissionActionAppealReview       PermissionAction = "appeal.review"
	PermissionActionTicketResolve      PermissionAction = "ticket.resolve"
	PermissionActionAuditRead          PermissionAction = "audit.read"
	PermissionActionGuildSettingsRead  PermissionAction = "guild_settings.read"
	PermissionActionGuildSettingsWrite PermissionAction = "guild_settings.write"
	PermissionActionCaseVoid           PermissionAction = "case.void"
	PermissionActionFailureDismiss     PermissionAction = "action_failure.dismiss"
)

// CaseValidity says whether a case still counts toward escalation.
type CaseValidity string

// Case validity values. A voided case is kept for history but no longer
// counts toward escalation.
const (
	CaseValidityValid  CaseValidity = "valid"
	CaseValidityVoided CaseValidity = "voided"
)

// CaseSource records where a case was created.
type CaseSource string

// Case sources.
const (
	CaseSourceDashboard CaseSource = "dashboard"
	CaseSourceDiscord   CaseSource = "discord"
	CaseSourceHoneypot  CaseSource = "honeypot"
	CaseSourceV4Import  CaseSource = "v4_import"
)

// ActionType is a Discord action Quack can perform against a member.
type ActionType string

// Action types. Templates may use timeout, kick, and ban. Remove-timeout and
// unban only exist as staff-confirmed reversals. Send-DM is kept for
// executions created before notifications moved onto the case.
const (
	ActionSendDM        ActionType = "send_dm"
	ActionTimeoutUser   ActionType = "timeout_user"
	ActionKickUser      ActionType = "kick_user"
	ActionBanUser       ActionType = "ban_user"
	ActionRemoveTimeout ActionType = "remove_timeout"
	ActionUnbanUser     ActionType = "unban_user"
)

// Label returns the human-readable name used in Discord and member messages.
// Stored values and JSON keep the stable identifier.
func (a ActionType) Label() string {
	switch a {
	case ActionTimeoutUser:
		return "Timeout"
	case ActionKickUser:
		return "Kick"
	case ActionBanUser:
		return "Ban"
	case ActionRemoveTimeout:
		return "Remove timeout"
	case ActionUnbanUser:
		return "Unban"
	case ActionSendDM:
		return "Notification"
	default:
		return "Action"
	}
}

// Irreversible reports whether a failed or interrupted attempt of this action
// must go to staff review instead of being repeated blindly. Every action
// that changes a member's standing in the guild qualifies.
func (a ActionType) Irreversible() bool {
	switch a {
	case ActionTimeoutUser, ActionKickUser, ActionBanUser, ActionRemoveTimeout, ActionUnbanUser:
		return true
	default:
		return false
	}
}

// ContextFieldType is the value shape of a template context field.
type ContextFieldType string

// Context field types.
const (
	ContextFieldShortText   ContextFieldType = "short_text"
	ContextFieldLongText    ContextFieldType = "long_text"
	ContextFieldBoolean     ContextFieldType = "boolean"
	ContextFieldNumber      ContextFieldType = "number"
	ContextFieldMessageLink ContextFieldType = "discord_message_link"
)

// NotificationStatus tracks the single member notification a case may send.
type NotificationStatus string

// Notification statuses. Prepared means a DM channel was opened before a
// kick or ban removed the member's shared guild.
const (
	NotificationPending  NotificationStatus = "pending"
	NotificationPrepared NotificationStatus = "prepared"
	NotificationClaimed  NotificationStatus = "claimed"
	NotificationSending  NotificationStatus = "sending"
	NotificationSent     NotificationStatus = "sent"
	NotificationFailed   NotificationStatus = "failed"
)

// ActionExecutionStatus tracks a queued action through the worker.
type ActionExecutionStatus string

// Action execution statuses.
const (
	ActionExecutionPending   ActionExecutionStatus = "pending"
	ActionExecutionRunning   ActionExecutionStatus = "running"
	ActionExecutionSucceeded ActionExecutionStatus = "succeeded"
	ActionExecutionFailed    ActionExecutionStatus = "failed"
	ActionExecutionRetrying  ActionExecutionStatus = "retrying"
	ActionExecutionCancelled ActionExecutionStatus = "cancelled"
)

// Label describes execution progress to members and staff without exposing
// internal state names.
func (s ActionExecutionStatus) Label() string {
	switch s {
	case ActionExecutionPending:
		return "Queued"
	case ActionExecutionRunning:
		return "In progress"
	case ActionExecutionSucceeded:
		return "Completed"
	case ActionExecutionFailed:
		return "Needs review"
	case ActionExecutionRetrying:
		return "Retry scheduled"
	case ActionExecutionCancelled:
		return "Cancelled"
	default:
		return "Unknown"
	}
}

// ActionAttemptStatus is the outcome of one call to Discord for an execution.
type ActionAttemptStatus string

// Action attempt statuses.
const (
	ActionAttemptRunning   ActionAttemptStatus = "running"
	ActionAttemptSucceeded ActionAttemptStatus = "succeeded"
	ActionAttemptFailed    ActionAttemptStatus = "failed"
)

// EventVisibility controls who can see a case timeline event.
type EventVisibility string

// Event visibilities. Members only ever see public events.
const (
	EventVisibilityInternal EventVisibility = "internal"
	EventVisibilityStaff    EventVisibility = "staff"
	EventVisibilityPublic   EventVisibility = "public"
)

// CaseEventType names a step in a case timeline.
type CaseEventType string

// Case event types.
const (
	CaseEventCreated            CaseEventType = "case_created"
	CaseEventActionQueued       CaseEventType = "action_queued"
	CaseEventActionSucceeded    CaseEventType = "action_succeeded"
	CaseEventActionFailed       CaseEventType = "action_failed"
	CaseEventVoided             CaseEventType = "case_voided"
	CaseEventReplaced           CaseEventType = "case_replaced"
	CaseEventActionRetried      CaseEventType = "action_retry_requested"
	CaseEventActionDismissed    CaseEventType = "action_failure_dismissed"
	CaseEventReversalQueued     CaseEventType = "action_reversal_queued"
	CaseEventNotificationSent   CaseEventType = "notification_sent"
	CaseEventNotificationFailed CaseEventType = "notification_failed"
	CaseEventAppealCreated      CaseEventType = "appeal_created"
)

// TicketStatus is the lifecycle state of a support ticket.
type TicketStatus string

// Ticket statuses.
const (
	TicketStatusOpen      TicketStatus = "open"
	TicketStatusResolved  TicketStatus = "resolved"
	TicketStatusCancelled TicketStatus = "cancelled"
)

// ULIDModel is the identity and timestamps every record carries. IDs are
// ULIDs so they sort by creation time.
type ULIDModel struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Guild is a Discord server Quack has been installed in.
type Guild struct {
	ULIDModel
	DiscordGuildID     string
	Name               string
	IconURL            string
	OwnerDiscordUserID string
	IsActive           bool
}

// GuildSettings is a guild's core configuration: managed channels,
// notification branding, module toggles, and starter policy state.
type GuildSettings struct {
	ULIDModel
	GuildID                           string
	AuditMirrorChannelDiscordID       string
	ManagedEvidenceChannelDiscordID   string
	NotificationIntroduction          string
	NotificationFooter                string
	TicketsEnabled                    bool
	GeneralLoggingEnabled             bool
	HoneypotEnabled                   bool
	StarterPolicyTemplateID           string
	StarterPolicyNoticePending        bool
	StarterPolicyNoticeAcknowledgedAt *time.Time
}

// StaffMember caches the last seen identity and permissions of someone who
// acted as staff in a guild. It is for attribution only; authorization always
// uses live Discord state.
type StaffMember struct {
	ULIDModel
	GuildID                string
	DiscordUserID          string
	LastSeenPermissionBits uint64
	LastKnownDisplayName   string
	LastActiveAt           *time.Time
}

// CaseTemplate is an admin-defined rule: the official reason, whether cases
// can be appealed, and (through its levels) what happens on each offense.
type CaseTemplate struct {
	ULIDModel
	GuildID                string
	Slug                   string
	Name                   string
	Description            string
	ReasonTemplate         string
	Appealable             bool
	Version                uint
	CreatedByDiscordUserID string
	UpdatedByDiscordUserID string
	ArchivedAt             *time.Time
}

// CaseTemplateContextField is a member-visible value moderators fill in
// whenever they apply the template.
type CaseTemplateContextField struct {
	ULIDModel
	TemplateID string
	Key        string
	Label      string
	FieldType  ContextFieldType
	Position   int
	Required   bool
}

// CaseTemplateLevel is one escalation step. The default level applies until
// the member's case count reaches another level's TriggerCaseCount.
type CaseTemplateLevel struct {
	ULIDModel
	TemplateID       string
	Position         int
	Name             string
	IsDefault        bool
	TriggerCaseCount int
	NotifyUser       bool
}

// CaseTemplateLevelAction is the enforcement a level performs. A level has
// at most one.
type CaseTemplateLevelAction struct {
	ULIDModel
	LevelID    string
	ActionType ActionType
	ConfigJSON string
	MaxRetries uint8
}

// Case is one application of a template to a member. Everything that
// decided the outcome is frozen in TemplateSnapshotJSON so later template
// edits never rewrite history.
type Case struct {
	ULIDModel
	GuildID                 string
	CaseNumber              uint64
	TemplateID              *string
	TemplateVersion         uint
	TemplateSnapshotJSON    string
	TargetDiscordUserID     string
	ModeratorDiscordUserID  string
	Reason                  string
	Validity                CaseValidity `gorm:"column:status"`
	Source                  CaseSource
	CorrelationID           string
	ContextChannelDiscordID string
	ContextMessageDiscordID string
	ContextURL              string
	MetadataJSON            string
	ContextValuesJSON       string
	VoidedReason            string
	VoidedByDiscordUserID   string
	VoidedAt                *time.Time
	ReplacementCaseID       *string
	ReplacesCaseID          *string
	IdempotencyKey          *string
}

// CaseActionExecution is a durable unit of enforcement work. The worker
// claims it with a lease, records each attempt, and leaves failures for
// staff review.
type CaseActionExecution struct {
	ULIDModel
	CaseID           string
	TemplateActionID *string
	Position         int
	ActionType       ActionType
	Status           ActionExecutionStatus
	IdempotencyKey   string
	// ConfigSnapshotJSON is the action configuration copied from the template
	// level at case creation.
	ConfigSnapshotJSON string
	NotifyUser         bool
	NotificationType   string
	AttemptCount       uint8
	MaxRetries         uint8
	RetryBackoffMS     int
	// SafeForRetry is true for template actions and false for reversals, which
	// staff must re-request explicitly.
	SafeForRetry             bool
	LastErrorCode            string
	LastError                string
	StartedAt                *time.Time
	FinishedAt               *time.Time
	NextRetryAt              *time.Time
	CorrelationID            string
	LeaseToken               string
	LeaseExpiresAt           *time.Time
	DismissedAt              *time.Time
	DismissedByDiscordUserID string
	ReversalOfExecutionID    *string
	ReversalAppealID         *string
}

// CaseNotification is the one automatic DM a case sends to its member. It
// belongs to the case, not to an action, so it is sent once after
// enforcement settles.
type CaseNotification struct {
	ULIDModel
	CaseID                   string
	Status                   NotificationStatus
	PreparedChannelDiscordID string
	RenderedMessage          string
	DeliveryMessageDiscordID string
	AttemptCount             uint8
	LastErrorCode            string
	LastError                string
	LeaseToken               string
	LeaseExpiresAt           *time.Time
	SentAt                   *time.Time
}

// CaseActionAttempt is one recorded call to Discord for an execution.
type CaseActionAttempt struct {
	ULIDModel
	ExecutionID         string
	AttemptNumber       uint8
	Status              ActionAttemptStatus
	WorkerID            string
	StartedAt           time.Time
	FinishedAt          *time.Time
	DurationMS          int64
	ErrorCode           string
	ErrorMessage        string
	RequestPayloadJSON  string
	ResponsePayloadJSON string
}

// CaseEvent is one entry in a case timeline.
type CaseEvent struct {
	ULIDModel
	CaseID             string
	GuildID            string
	EventType          CaseEventType
	ActorDiscordUserID string
	ActorType          string
	Visibility         EventVisibility
	Body               string
	MetadataJSON       string
}

// AuditLogEntry is one append-only record of something that happened in a
// guild, including denials and failures.
type AuditLogEntry struct {
	ULIDModel
	GuildID             string
	ActorDiscordUserID  string
	ActorPermissionBits uint64
	Source              AuditSource
	Action              string
	ResourceType        string
	ResourceID          string
	Result              AuditResult
	FailureReason       string
	CorrelationID       string
	RequestID           string
	MetadataJSON        string
}

// OAuthState is the server-side half of a Discord OAuth login in progress.
type OAuthState struct {
	RedirectTo   string    `json:"redirect_to"`
	ResponseMode string    `json:"response_mode"`
	CreatedAt    time.Time `json:"created_at"`
}

// AuthSession is a signed-in dashboard user. Tokens never leave the server.
type AuthSession struct {
	ID               string    `json:"-"`
	DiscordUserID    string    `json:"discord_user_id"`
	Username         string    `json:"username"`
	GlobalName       string    `json:"global_name"`
	Avatar           string    `json:"avatar"`
	AccessToken      string    `json:"-"`
	RefreshToken     string    `json:"-"`
	CSRFToken        string    `json:"-"`
	TokenType        string    `json:"token_type"`
	Scope            string    `json:"scope"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
}
