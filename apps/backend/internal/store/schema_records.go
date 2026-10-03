package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// ULIDModelRecord is the GORM persistence representation of ulidmodel; domain models remain storage-agnostic.
type ULIDModelRecord struct {
	ID        string    `gorm:"type:char(26);primaryKey"`
	CreatedAt time.Time `gorm:"not null;index"`
	UpdatedAt time.Time `gorm:"not null"`
}

// GuildRecord is the GORM persistence representation of guild; domain models remain storage-agnostic.
type GuildRecord struct {
	ULIDModelRecord
	DiscordGuildID     string `gorm:"size:32;not null;uniqueIndex"`
	Name               string `gorm:"size:191;not null"`
	IconURL            string `gorm:"size:1024"`
	OwnerDiscordUserID string `gorm:"size:32;not null;index"`
	IsActive           bool   `gorm:"not null;default:true;index"`
}

// GuildSettingsRecord is the GORM persistence representation of guild-owned setup and module enablement state.
type GuildSettingsRecord struct {
	ULIDModelRecord
	GuildID                           string     `gorm:"type:char(26);not null;uniqueIndex"`
	AuditMirrorChannelDiscordID       string     `gorm:"size:32;not null;default:''"`
	ManagedEvidenceChannelDiscordID   string     `gorm:"size:32;not null;default:''"`
	NotificationIntroduction          string     `gorm:"type:text;not null"`
	NotificationFooter                string     `gorm:"type:text;not null"`
	TicketsEnabled                    bool       `gorm:"not null;default:false"`
	GeneralLoggingEnabled             bool       `gorm:"not null;default:false"`
	HoneypotEnabled                   bool       `gorm:"not null;default:false"`
	StarterPolicyTemplateID           string     `gorm:"type:char(26);not null;default:''"`
	StarterPolicyNoticePending        bool       `gorm:"not null;default:true"`
	StarterPolicyNoticeAcknowledgedAt *time.Time `gorm:"index"`
}

// TableName keeps the adapter record aligned with migration 0004's guild_settings table.
func (GuildSettingsRecord) TableName() string { return "guild_settings" }

// StaffMemberRecord is the GORM persistence representation of staff member; domain models remain storage-agnostic.
type StaffMemberRecord struct {
	ULIDModelRecord
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_staff_member_guild_user,priority:1;index"`
	DiscordUserID          string     `gorm:"size:32;not null;uniqueIndex:idx_staff_member_guild_user,priority:2;index"`
	LastSeenPermissionBits uint64     `gorm:"type:bigint unsigned;not null;default:0"`
	LastKnownDisplayName   string     `gorm:"size:191"`
	LastActiveAt           *time.Time `gorm:"index"`
}

// CaseTemplateRecord is the GORM persistence representation of case template; domain models remain storage-agnostic.
type CaseTemplateRecord struct {
	ULIDModelRecord
	GuildID                string     `gorm:"type:char(26);not null;uniqueIndex:idx_case_template_guild_slug,priority:1;index:idx_case_template_guild_enabled,priority:1"`
	Slug                   string     `gorm:"size:64;not null;uniqueIndex:idx_case_template_guild_slug,priority:2"`
	Name                   string     `gorm:"size:191;not null"`
	Description            string     `gorm:"type:text;not null"`
	ReasonTemplate         string     `gorm:"type:text;not null"`
	DefaultSeverity        string     `gorm:"size:32;not null;default:'medium'"`
	Appealable             bool       `gorm:"not null;default:false"`
	Enabled                bool       `gorm:"not null;default:true;index:idx_case_template_guild_enabled,priority:2"`
	Version                uint       `gorm:"not null;default:1"`
	CreatedByDiscordUserID string     `gorm:"size:32;not null"`
	UpdatedByDiscordUserID string     `gorm:"size:32;not null"`
	ArchivedAt             *time.Time `gorm:"index"`
	// DeletedAt preserves the frozen v4-era column without enabling GORM soft-delete behavior in live v5 queries.
	DeletedAt *time.Time `gorm:"index"`
}

// CaseTemplateContextFieldRecord persists an ordered template context definition.
type CaseTemplateContextFieldRecord struct {
	ULIDModelRecord
	TemplateID string                 `gorm:"type:char(26);not null;uniqueIndex:idx_template_context_key,priority:1;uniqueIndex:idx_template_context_position,priority:1;index"`
	Key        string                 `gorm:"size:64;not null;uniqueIndex:idx_template_context_key,priority:2"`
	Label      string                 `gorm:"size:191;not null"`
	FieldType  quack.ContextFieldType `gorm:"size:32;not null"`
	Position   int                    `gorm:"not null;uniqueIndex:idx_template_context_position,priority:2"`
	Required   bool                   `gorm:"not null;default:false"`
}

// CaseTemplateLevelRecord is the GORM persistence representation of case template level; domain models remain storage-agnostic.
type CaseTemplateLevelRecord struct {
	ULIDModelRecord
	TemplateID       string `gorm:"type:char(26);not null;uniqueIndex:idx_template_level_position,priority:1;index"`
	Position         int    `gorm:"not null;uniqueIndex:idx_template_level_position,priority:2"`
	Name             string `gorm:"size:191;not null"`
	IsDefault        bool   `gorm:"not null;default:false;index"`
	TriggerCaseCount int    `gorm:"not null;default:0"`
	WindowMinutes    int    `gorm:"not null;default:0"`
	NotifyUser       bool   `gorm:"not null;default:false"`
	NotificationType string `gorm:"size:64"`
	Enabled          bool   `gorm:"not null;default:true"`
}

// CaseTemplateLevelActionRecord is the GORM persistence representation of case template level action; domain models remain storage-agnostic.
type CaseTemplateLevelActionRecord struct {
	ULIDModelRecord
	LevelID          string           `gorm:"type:char(26);not null;uniqueIndex:idx_level_action_position,priority:1;index"`
	Position         int              `gorm:"not null;uniqueIndex:idx_level_action_position,priority:2"`
	ActionType       quack.ActionType `gorm:"size:64;not null;index"`
	ConfigJSON       string           `gorm:"type:json;not null"`
	NotifyUser       bool             `gorm:"not null;default:false"`
	NotificationType string           `gorm:"size:64"`
	ContinueOnError  bool             `gorm:"not null;default:false"`
	MaxRetries       uint8            `gorm:"not null;default:0"`
	RetryBackoffMS   int              `gorm:"not null;default:0"`
	TimeoutMS        int              `gorm:"not null;default:0"`
	IdempotencyScope string           `gorm:"size:32;not null;default:'case'"`
	Enabled          bool             `gorm:"not null;default:true"`
}

// CaseRecord is the GORM persistence representation of case; domain models remain storage-agnostic.
type CaseRecord struct {
	ULIDModelRecord
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_case_guild_case_number,priority:1,unique;index:idx_case_guild_target,priority:1;index:idx_case_guild_mod,priority:1;index:idx_case_guild_status,priority:1;uniqueIndex:idx_case_guild_idempotency,priority:1"`
	CaseNumber              uint64             `gorm:"type:bigint unsigned;not null;index:idx_case_guild_case_number,priority:2,unique"`
	TemplateID              *string            `gorm:"type:char(26);index"`
	TemplateVersion         uint               `gorm:"not null;default:1"`
	TemplateSnapshotJSON    string             `gorm:"type:json;not null"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_case_guild_target,priority:2"`
	ModeratorDiscordUserID  string             `gorm:"size:32;not null;index:idx_case_guild_mod,priority:2"`
	Reason                  string             `gorm:"type:text;not null"`
	Validity                quack.CaseValidity `gorm:"column:status;size:32;not null;default:'valid';index:idx_case_guild_status,priority:2"`
	Source                  quack.CaseSource   `gorm:"size:32;not null;default:'dashboard';index"`
	CorrelationID           string             `gorm:"size:128;index"`
	ContextChannelDiscordID string             `gorm:"size:32"`
	ContextMessageDiscordID string             `gorm:"size:32"`
	ContextURL              string             `gorm:"size:1024"`
	MetadataJSON            string             `gorm:"type:json;not null"`
	ContextValuesJSON       string             `gorm:"type:json"`
	VoidedReason            string             `gorm:"type:text"`
	VoidedByDiscordUserID   string             `gorm:"size:32;not null;default:''"`
	VoidedAt                *time.Time         `gorm:"index"`
	ReplacementCaseID       *string            `gorm:"type:char(26);index"`
	ReplacesCaseID          *string            `gorm:"type:char(26);index"`
	IdempotencyKey          *string            `gorm:"size:191;uniqueIndex:idx_case_guild_idempotency,priority:2"`
}

// CaseActionExecutionRecord is the GORM persistence representation of case action execution; domain models remain storage-agnostic.
type CaseActionExecutionRecord struct {
	ULIDModelRecord
	CaseID                   string                      `gorm:"type:char(26);not null;index:idx_action_execution_case_position,priority:1;index"`
	TemplateActionID         *string                     `gorm:"type:char(26);index"`
	Position                 int                         `gorm:"not null;index:idx_action_execution_case_position,priority:2"`
	ActionType               quack.ActionType            `gorm:"size:64;not null;index"`
	Status                   quack.ActionExecutionStatus `gorm:"size:32;not null;default:'pending';index:idx_action_execution_status_retry,priority:1"`
	IdempotencyKey           string                      `gorm:"size:191;not null;uniqueIndex"`
	ConfigSnapshotJSON       string                      `gorm:"type:json;not null"`
	NotifyUser               bool                        `gorm:"not null;default:false"`
	NotificationType         string                      `gorm:"size:64"`
	AttemptCount             uint8                       `gorm:"not null;default:0"`
	MaxRetries               uint8                       `gorm:"not null;default:0"`
	RetryBackoffMS           int                         `gorm:"not null;default:0"`
	SafeForRetry             bool                        `gorm:"not null;default:true"`
	Irreversible             bool                        `gorm:"not null;default:false"`
	LastErrorCode            string                      `gorm:"size:64"`
	LastError                string                      `gorm:"type:text"`
	StartedAt                *time.Time
	FinishedAt               *time.Time
	NextRetryAt              *time.Time `gorm:"index:idx_action_execution_status_retry,priority:2"`
	CorrelationID            string     `gorm:"size:128;index"`
	LeaseToken               string     `gorm:"size:64;index"`
	LeaseExpiresAt           *time.Time `gorm:"index"`
	DismissedAt              *time.Time `gorm:"index"`
	DismissedByDiscordUserID string     `gorm:"size:32;not null;default:''"`
	ReversalOfExecutionID    *string    `gorm:"type:char(26);index"`
	ReversalAppealID         *string    `gorm:"type:char(26);index"`
}

// CaseEvidenceSnapshotRecord persists immutable Discord message evidence.
type CaseEvidenceSnapshotRecord struct {
	ULIDModelRecord
	CaseID              string    `gorm:"type:char(26);not null;index"`
	GuildID             string    `gorm:"type:char(26);not null;index"`
	ChannelDiscordID    string    `gorm:"size:32;not null"`
	MessageDiscordID    string    `gorm:"size:32;not null"`
	AuthorDiscordUserID string    `gorm:"size:32;not null"`
	MessageURL          string    `gorm:"size:1024;not null"`
	Content             string    `gorm:"type:text;not null"`
	MessageCreatedAt    time.Time `gorm:"not null"`
	MessageEditedAt     *time.Time
	EmbedsJSON          string `gorm:"type:json;not null"`
	CaptureOutcome      string `gorm:"size:32;not null"`
	CaptureWarning      string `gorm:"type:text;not null"`
}

// CaseEvidenceAttachmentRecord persists original metadata and optional managed copy references.
type CaseEvidenceAttachmentRecord struct {
	ULIDModelRecord
	EvidenceID                   string `gorm:"type:char(26);not null;index"`
	Filename                     string `gorm:"size:255;not null"`
	ContentType                  string `gorm:"size:191;not null"`
	SizeBytes                    int64  `gorm:"not null"`
	OriginalURL                  string `gorm:"size:2048;not null"`
	PreservedURL                 string `gorm:"size:2048;not null"`
	PreservedMessageDiscordID    string `gorm:"size:32;not null"`
	PreservedAttachmentDiscordID string `gorm:"size:32;not null"`
	CopyOutcome                  string `gorm:"size:32;not null"`
	Warning                      string `gorm:"type:text;not null"`
}

// CaseNotificationRecord persists the single automatic case notification and its fenced delivery state.
type CaseNotificationRecord struct {
	ULIDModelRecord
	CaseID                   string                   `gorm:"type:char(26);not null;uniqueIndex"`
	Status                   quack.NotificationStatus `gorm:"size:32;not null;index"`
	PreparedChannelDiscordID string                   `gorm:"size:32;not null"`
	RenderedMessage          string                   `gorm:"type:text;not null"`
	DeliveryMessageDiscordID string                   `gorm:"size:32;not null"`
	AttemptCount             uint8                    `gorm:"not null;default:0"`
	LastErrorCode            string                   `gorm:"size:64;not null"`
	LastError                string                   `gorm:"type:text;not null"`
	LeaseToken               string                   `gorm:"size:64;index"`
	LeaseExpiresAt           *time.Time               `gorm:"index"`
	SentAt                   *time.Time
}

// CaseActionAttemptRecord is the GORM persistence representation of case action attempt; domain models remain storage-agnostic.
type CaseActionAttemptRecord struct {
	ULIDModelRecord
	ExecutionID         string                    `gorm:"type:char(26);not null;uniqueIndex:idx_action_attempt_execution_number,priority:1;index"`
	AttemptNumber       uint8                     `gorm:"not null;uniqueIndex:idx_action_attempt_execution_number,priority:2"`
	Status              quack.ActionAttemptStatus `gorm:"size:32;not null;index"`
	WorkerID            string                    `gorm:"size:64"`
	StartedAt           time.Time                 `gorm:"not null"`
	FinishedAt          *time.Time
	DurationMS          int64  `gorm:"not null;default:0"`
	ErrorCode           string `gorm:"size:64"`
	ErrorMessage        string `gorm:"type:text"`
	RequestPayloadJSON  string `gorm:"type:json;not null"`
	ResponsePayloadJSON string `gorm:"type:json;not null"`
}

// CaseEventRecord is the GORM persistence representation of case event; domain models remain storage-agnostic.
type CaseEventRecord struct {
	ULIDModelRecord
	CaseID             string                `gorm:"type:char(26);not null;index:idx_case_event_case_created,priority:1"`
	GuildID            string                `gorm:"type:char(26);not null;index"`
	EventType          quack.CaseEventType   `gorm:"size:64;not null;index"`
	ActorDiscordUserID string                `gorm:"size:32;index"`
	ActorType          string                `gorm:"size:32;not null;default:'system'"`
	Visibility         quack.EventVisibility `gorm:"size:32;not null;default:'staff'"`
	Body               string                `gorm:"type:text;not null"`
	MetadataJSON       string                `gorm:"type:json;not null"`
}

// AppealRecord is the GORM persistence representation of appeal; domain models remain storage-agnostic.
type AppealRecord struct {
	ULIDModelRecord
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string            `gorm:"type:char(26);index"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  quack.AppealStatus `gorm:"size:32;not null;default:'pending';index:idx_appeal_guild_status,priority:2"`
	Content                 string             `gorm:"type:text;not null"`
	DecisionReason          string             `gorm:"type:text"`
	ReviewedByDiscordUserID string             `gorm:"size:32"`
	ReviewedAt              *time.Time         `gorm:"index"`
	ReviewMessageDiscordID  string             `gorm:"size:32"`
	MetadataJSON            string             `gorm:"type:json;not null"`
}

// AppealEventRecord is the GORM persistence representation of appeal event; domain models remain storage-agnostic.
type AppealEventRecord struct {
	ULIDModelRecord
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// GuildAppealSettingsRecord persists the validated form used by future appeals in one guild.
type GuildAppealSettingsRecord struct {
	ULIDModelRecord
	GuildID                string `gorm:"type:char(26);not null;uniqueIndex"`
	QuestionsJSON          string `gorm:"type:json;not null"`
	UpdatedByDiscordUserID string `gorm:"size:32;not null"`
}

// AppealNotificationRecord persists one idempotent member or staff outbox message per timeline event.
type AppealNotificationRecord struct {
	ULIDModelRecord
	AppealID            string                           `gorm:"type:char(26);not null;index"`
	EventID             string                           `gorm:"type:char(26);not null;uniqueIndex"`
	GuildID             string                           `gorm:"type:char(26);not null;index"`
	TargetDiscordUserID string                           `gorm:"size:32;not null;index"`
	Audience            quack.AppealNotificationAudience `gorm:"size:32;not null;index"`
	Status              quack.AppealNotificationStatus   `gorm:"size:32;not null;index"`
	Body                string                           `gorm:"type:text;not null"`
	DeliveryMessageID   string                           `gorm:"size:32;not null"`
	LastErrorCode       string                           `gorm:"size:64;not null"`
	LeaseToken          string                           `gorm:"size:64;not null;index"`
	LeaseExpiresAt      *time.Time                       `gorm:"index"`
}

// TicketRecord is the GORM persistence representation of ticket; domain models remain storage-agnostic.
type TicketRecord struct {
	ULIDModelRecord
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_ticket_guild_status,priority:1;index:idx_ticket_guild_owner,priority:1"`
	OwnerDiscordUserID      string             `gorm:"size:32;not null;index:idx_ticket_guild_owner,priority:2"`
	ThreadDiscordChannelID  string             `gorm:"size:32;uniqueIndex"`
	Status                  quack.TicketStatus `gorm:"size:32;not null;default:'open';index:idx_ticket_guild_status,priority:2"`
	LogMessageDiscordID     string             `gorm:"size:32"`
	ResolvedByDiscordUserID string             `gorm:"size:32"`
	ResolvedAt              *time.Time         `gorm:"index"`
	TranscriptURL           string             `gorm:"size:1024"`
	MetadataJSON            string             `gorm:"type:json;not null"`
}

// TicketEventRecord is the GORM persistence representation of ticket event; domain models remain storage-agnostic.
type TicketEventRecord struct {
	ULIDModelRecord
	TicketID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// AuditLogEntryRecord is the GORM persistence representation of audit log entry; domain models remain storage-agnostic.
type AuditLogEntryRecord struct {
	ULIDModelRecord
	GuildID             string            `gorm:"type:char(26);not null;index:idx_audit_guild_action,priority:1;index"`
	ActorDiscordUserID  string            `gorm:"size:32;index"`
	ActorPermissionBits uint64            `gorm:"type:bigint unsigned;not null;default:0"`
	Source              quack.AuditSource `gorm:"size:32;not null;index"`
	Action              string            `gorm:"size:96;not null;index:idx_audit_guild_action,priority:2"`
	ResourceType        string            `gorm:"size:64;not null;index:idx_audit_resource,priority:1"`
	ResourceID          string            `gorm:"size:64;not null;index:idx_audit_resource,priority:2"`
	Result              quack.AuditResult `gorm:"size:32;not null;index"`
	FailureReason       string            `gorm:"type:text"`
	CorrelationID       string            `gorm:"size:128;index"`
	RequestID           string            `gorm:"size:128;index"`
	MetadataJSON        string            `gorm:"type:json;not null"`
}

// schemaModels encapsulates the schema models rule so callers share one consistent package implementation.
func schemaModels() []any {
	return []any{
		&GuildRecord{},
		&StaffMemberRecord{},
		&CaseTemplateRecord{},
		&CaseTemplateContextFieldRecord{},
		&CaseTemplateLevelRecord{},
		&CaseTemplateLevelActionRecord{},
		&CaseRecord{},
		&CaseActionExecutionRecord{},
		&CaseActionAttemptRecord{},
		&CaseEvidenceSnapshotRecord{},
		&CaseEvidenceAttachmentRecord{},
		&CaseNotificationRecord{},
		&CaseEventRecord{},
		&AppealRecord{},
		&AppealEventRecord{},
		&TicketRecord{},
		&TicketEventRecord{},
		&AuditLogEntryRecord{},
	}
}

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (GuildRecord) TableName() string { return "guilds" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (StaffMemberRecord) TableName() string { return "staff_members" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseTemplateRecord) TableName() string { return "case_templates" }

// TableName returns the migration-owned template context table.
func (CaseTemplateContextFieldRecord) TableName() string { return "case_template_context_fields" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseTemplateLevelRecord) TableName() string { return "case_template_levels" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseTemplateLevelActionRecord) TableName() string { return "case_template_level_actions" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseRecord) TableName() string { return "cases" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseActionExecutionRecord) TableName() string { return "case_action_executions" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseActionAttemptRecord) TableName() string { return "case_action_attempts" }

// TableName returns the migration-owned case evidence table.
func (CaseEvidenceSnapshotRecord) TableName() string { return "case_evidence_snapshots" }

// TableName returns the migration-owned attachment table.
func (CaseEvidenceAttachmentRecord) TableName() string { return "case_evidence_attachments" }

// TableName returns the migration-owned notification table.
func (CaseNotificationRecord) TableName() string { return "case_notifications" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (CaseEventRecord) TableName() string { return "case_events" }

// TableName identifies the logical 0200 guild appeal settings table.
func (GuildAppealSettingsRecord) TableName() string { return "guild_appeal_settings" }

// TableName identifies the logical 0200 appeal notification outbox.
func (AppealNotificationRecord) TableName() string { return "appeal_notifications" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (AppealRecord) TableName() string { return "appeals" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (AppealEventRecord) TableName() string { return "appeal_events" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (TicketRecord) TableName() string { return "tickets" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (TicketEventRecord) TableName() string { return "ticket_events" }

// TableName preserves the pre-refactor table name so migrations and existing v5 data remain compatible.
func (AuditLogEntryRecord) TableName() string { return "audit_log_entries" }
