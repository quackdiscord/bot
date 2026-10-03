package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// This file is the database schema: one record struct per table, and tables
// listing them for the baseline migration in migrate.go. Records never leave
// the package. Each concept file maps its records to and from the quack
// domain types with a newXRecord constructor and a model method.
//
// IDs are ULIDs and timestamps are UTC. Index names are prefixed with their
// table because SQLite index names share one namespace across the database.

// guildRecord is a Discord server Quack is or was installed in. Leaving a
// guild only clears IsActive; its history stays.
type guildRecord struct {
	ID                 string    `gorm:"type:char(26);primaryKey"`
	CreatedAt          time.Time `gorm:"not null"`
	UpdatedAt          time.Time `gorm:"not null"`
	DiscordGuildID     string    `gorm:"size:32;not null;uniqueIndex"`
	Name               string    `gorm:"size:191;not null"`
	IconURL            string    `gorm:"size:1024;not null;default:''"`
	OwnerDiscordUserID string    `gorm:"size:32;not null"`
	IsActive           bool      `gorm:"not null"`
}

func (guildRecord) TableName() string { return "guilds" }

// guildSettingsRecord is a guild's core settings. The optional modules'
// switches are not here: they live in module_configurations.
type guildSettingsRecord struct {
	ID                                string    `gorm:"type:char(26);primaryKey"`
	CreatedAt                         time.Time `gorm:"not null"`
	UpdatedAt                         time.Time `gorm:"not null"`
	GuildID                           string    `gorm:"type:char(26);not null;uniqueIndex"`
	AuditMirrorChannelDiscordID       string    `gorm:"size:32;not null;default:''"`
	ManagedEvidenceChannelDiscordID   string    `gorm:"size:32;not null;default:''"`
	NotificationIntroduction          string    `gorm:"type:text;not null"`
	NotificationFooter                string    `gorm:"type:text;not null"`
	StarterPolicyTemplateID           string    `gorm:"type:char(26);not null;default:''"`
	StarterPolicyNoticePending        bool      `gorm:"not null"`
	StarterPolicyNoticeAcknowledgedAt *time.Time
}

func (guildSettingsRecord) TableName() string { return "guild_settings" }

// staffMemberRecord caches what Quack last saw of a moderator: permissions,
// display name, and when they were last active.
type staffMemberRecord struct {
	ID                     string    `gorm:"type:char(26);primaryKey"`
	CreatedAt              time.Time `gorm:"not null"`
	UpdatedAt              time.Time `gorm:"not null"`
	GuildID                string    `gorm:"type:char(26);not null;uniqueIndex:idx_staff_members_guild_user,priority:1"`
	DiscordUserID          string    `gorm:"size:32;not null;uniqueIndex:idx_staff_members_guild_user,priority:2"`
	LastSeenPermissionBits uint64    `gorm:"type:bigint unsigned;not null;default:0"`
	LastKnownDisplayName   string    `gorm:"size:191;not null;default:''"`
	LastActiveAt           *time.Time
}

func (staffMemberRecord) TableName() string { return "staff_members" }

// templateRecord is a case template. Version counts policy edits; cases keep
// a snapshot of the version they were created under.
type templateRecord struct {
	ID                     string    `gorm:"type:char(26);primaryKey"`
	CreatedAt              time.Time `gorm:"not null"`
	UpdatedAt              time.Time `gorm:"not null"`
	GuildID                string    `gorm:"type:char(26);not null;uniqueIndex:idx_case_templates_guild_slug,priority:1"`
	Slug                   string    `gorm:"size:64;not null;uniqueIndex:idx_case_templates_guild_slug,priority:2"`
	Name                   string    `gorm:"size:191;not null"`
	Description            string    `gorm:"type:text;not null"`
	ReasonTemplate         string    `gorm:"type:text;not null"`
	Appealable             bool      `gorm:"not null;default:false"`
	Version                uint      `gorm:"not null;default:1"`
	CreatedByDiscordUserID string    `gorm:"size:32;not null"`
	UpdatedByDiscordUserID string    `gorm:"size:32;not null"`
	ArchivedAt             *time.Time
}

func (templateRecord) TableName() string { return "case_templates" }

// contextFieldRecord is an extra input a template asks moderators for.
type contextFieldRecord struct {
	ID         string                 `gorm:"type:char(26);primaryKey"`
	CreatedAt  time.Time              `gorm:"not null"`
	UpdatedAt  time.Time              `gorm:"not null"`
	TemplateID string                 `gorm:"type:char(26);not null;uniqueIndex:idx_context_fields_template_key,priority:1;uniqueIndex:idx_context_fields_template_position,priority:1"`
	Key        string                 `gorm:"size:64;not null;uniqueIndex:idx_context_fields_template_key,priority:2"`
	Label      string                 `gorm:"size:191;not null"`
	FieldType  quack.ContextFieldType `gorm:"size:32;not null"`
	Position   int                    `gorm:"not null;uniqueIndex:idx_context_fields_template_position,priority:2"`
	Required   bool                   `gorm:"not null;default:false"`
}

func (contextFieldRecord) TableName() string { return "case_template_context_fields" }

// levelRecord is one escalation level. The baseline migration adds the
// one-default-per-template constraint, which struct tags cannot express.
type levelRecord struct {
	ID               string    `gorm:"type:char(26);primaryKey"`
	CreatedAt        time.Time `gorm:"not null"`
	UpdatedAt        time.Time `gorm:"not null"`
	TemplateID       string    `gorm:"type:char(26);not null;uniqueIndex:idx_levels_template_position,priority:1"`
	Position         int       `gorm:"not null;uniqueIndex:idx_levels_template_position,priority:2"`
	Name             string    `gorm:"size:191;not null"`
	IsDefault        bool      `gorm:"not null;default:false"`
	TriggerCaseCount int       `gorm:"not null;default:0"`
	NotifyUser       bool      `gorm:"not null;default:false"`
}

func (levelRecord) TableName() string { return "case_template_levels" }

// levelActionRecord is a level's enforcement. The unique level_id keeps it
// to one action per level.
type levelActionRecord struct {
	ID         string           `gorm:"type:char(26);primaryKey"`
	CreatedAt  time.Time        `gorm:"not null"`
	UpdatedAt  time.Time        `gorm:"not null"`
	LevelID    string           `gorm:"type:char(26);not null;uniqueIndex"`
	ActionType quack.ActionType `gorm:"size:64;not null"`
	ConfigJSON string           `gorm:"type:json;not null"`
	MaxRetries uint8            `gorm:"not null;default:0"`
}

func (levelActionRecord) TableName() string { return "case_template_level_actions" }

// caseRecord is a moderation case. Cases are numbered per guild and never
// deleted; voiding one keeps it but stops it counting toward escalation.
type caseRecord struct {
	ID                      string             `gorm:"type:char(26);primaryKey"`
	CreatedAt               time.Time          `gorm:"not null;index:idx_cases_guild_target,priority:3;index:idx_cases_guild_created,priority:2"`
	UpdatedAt               time.Time          `gorm:"not null"`
	GuildID                 string             `gorm:"type:char(26);not null;uniqueIndex:idx_cases_guild_number,priority:1;uniqueIndex:idx_cases_guild_idempotency,priority:1;index:idx_cases_guild_target,priority:1;index:idx_cases_guild_moderator,priority:1;index:idx_cases_guild_created,priority:1"`
	CaseNumber              uint64             `gorm:"type:bigint unsigned;not null;uniqueIndex:idx_cases_guild_number,priority:2"`
	TemplateID              *string            `gorm:"type:char(26);index"`
	TemplateVersion         uint               `gorm:"not null"`
	TemplateSnapshotJSON    string             `gorm:"type:json;not null"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_cases_guild_target,priority:2"`
	ModeratorDiscordUserID  string             `gorm:"size:32;not null;index:idx_cases_guild_moderator,priority:2"`
	Reason                  string             `gorm:"type:text;not null"`
	Validity                quack.CaseValidity `gorm:"size:32;not null;default:'valid'"`
	Source                  quack.CaseSource   `gorm:"size:32;not null"`
	CorrelationID           string             `gorm:"size:128;not null;default:''"`
	ContextChannelDiscordID string             `gorm:"size:32;not null;default:''"`
	ContextMessageDiscordID string             `gorm:"size:32;not null;default:''"`
	ContextURL              string             `gorm:"size:1024;not null;default:''"`
	MetadataJSON            string             `gorm:"type:json;not null"`
	ContextValuesJSON       string             `gorm:"type:json;not null"`
	VoidedReason            string             `gorm:"type:text"`
	VoidedByDiscordUserID   string             `gorm:"size:32;not null;default:''"`
	VoidedAt                *time.Time
	ReplacementCaseID       *string `gorm:"type:char(26)"`
	ReplacesCaseID          *string `gorm:"type:char(26)"`
	IdempotencyKey          *string `gorm:"size:191;uniqueIndex:idx_cases_guild_idempotency,priority:2"`
}

func (caseRecord) TableName() string { return "cases" }

// executionRecord is one Discord action a case enforces, run in Position
// order by the worker that holds LeaseToken until LeaseExpiresAt.
type executionRecord struct {
	ID                       string                      `gorm:"type:char(26);primaryKey"`
	CreatedAt                time.Time                   `gorm:"not null;index"`
	UpdatedAt                time.Time                   `gorm:"not null"`
	CaseID                   string                      `gorm:"type:char(26);not null;index:idx_executions_case_position,priority:1"`
	TemplateActionID         *string                     `gorm:"type:char(26)"`
	Position                 int                         `gorm:"not null;index:idx_executions_case_position,priority:2"`
	ActionType               quack.ActionType            `gorm:"size:64;not null"`
	Status                   quack.ActionExecutionStatus `gorm:"size:32;not null;default:'pending';index:idx_executions_claim,priority:1"`
	IdempotencyKey           string                      `gorm:"size:191;not null;uniqueIndex"`
	ConfigSnapshotJSON       string                      `gorm:"type:json;not null"`
	NotifyUser               bool                        `gorm:"not null;default:false"`
	NotificationType         string                      `gorm:"size:64;not null;default:''"`
	AttemptCount             uint8                       `gorm:"not null;default:0"`
	MaxRetries               uint8                       `gorm:"not null;default:0"`
	RetryBackoffMS           int                         `gorm:"not null;default:0"`
	SafeForRetry             bool                        `gorm:"not null"`
	LastErrorCode            string                      `gorm:"size:64;not null;default:''"`
	LastError                string                      `gorm:"type:text"`
	StartedAt                *time.Time
	FinishedAt               *time.Time
	NextRetryAt              *time.Time `gorm:"index:idx_executions_claim,priority:2"`
	CorrelationID            string     `gorm:"size:128;not null;default:''"`
	LeaseToken               string     `gorm:"size:64;not null;default:''"`
	LeaseExpiresAt           *time.Time `gorm:"index:idx_executions_claim,priority:3"`
	DismissedAt              *time.Time
	DismissedByDiscordUserID string  `gorm:"size:32;not null;default:''"`
	ReversalOfExecutionID    *string `gorm:"type:char(26)"`
	ReversalAppealID         *string `gorm:"type:char(26)"`
}

func (executionRecord) TableName() string { return "case_action_executions" }

// attemptRecord is one try at an execution, kept as history.
type attemptRecord struct {
	ID                  string                    `gorm:"type:char(26);primaryKey"`
	CreatedAt           time.Time                 `gorm:"not null"`
	UpdatedAt           time.Time                 `gorm:"not null"`
	ExecutionID         string                    `gorm:"type:char(26);not null;uniqueIndex:idx_attempts_execution_number,priority:1"`
	AttemptNumber       uint8                     `gorm:"not null;uniqueIndex:idx_attempts_execution_number,priority:2"`
	Status              quack.ActionAttemptStatus `gorm:"size:32;not null"`
	WorkerID            string                    `gorm:"size:64;not null;default:''"`
	StartedAt           time.Time                 `gorm:"not null"`
	FinishedAt          *time.Time
	DurationMS          int64  `gorm:"not null;default:0"`
	ErrorCode           string `gorm:"size:64;not null;default:''"`
	ErrorMessage        string `gorm:"type:text"`
	RequestPayloadJSON  string `gorm:"type:json;not null"`
	ResponsePayloadJSON string `gorm:"type:json;not null"`
}

func (attemptRecord) TableName() string { return "case_action_attempts" }

// evidenceRecord is a Discord message captured with a case, as it was when
// the case was created.
type evidenceRecord struct {
	ID                  string    `gorm:"type:char(26);primaryKey"`
	CreatedAt           time.Time `gorm:"not null"`
	UpdatedAt           time.Time `gorm:"not null"`
	CaseID              string    `gorm:"type:char(26);not null;index:idx_evidence_case_message,priority:1"`
	GuildID             string    `gorm:"type:char(26);not null"`
	ChannelDiscordID    string    `gorm:"size:32;not null"`
	MessageDiscordID    string    `gorm:"size:32;not null;index:idx_evidence_case_message,priority:2"`
	AuthorDiscordUserID string    `gorm:"size:32;not null"`
	MessageURL          string    `gorm:"size:1024;not null"`
	Content             string    `gorm:"type:text;not null"`
	MessageCreatedAt    time.Time `gorm:"not null"`
	MessageEditedAt     *time.Time
	EmbedsJSON          string `gorm:"type:json;not null"`
	CaptureOutcome      string `gorm:"size:32;not null"`
	CaptureWarning      string `gorm:"type:text;not null"`
}

func (evidenceRecord) TableName() string { return "case_evidence_snapshots" }

// attachmentRecord is a file on captured evidence, with where Quack
// re-uploaded it so it outlives the original message.
type attachmentRecord struct {
	ID                           string    `gorm:"type:char(26);primaryKey"`
	CreatedAt                    time.Time `gorm:"not null"`
	UpdatedAt                    time.Time `gorm:"not null"`
	EvidenceID                   string    `gorm:"type:char(26);not null;index"`
	Filename                     string    `gorm:"size:255;not null"`
	ContentType                  string    `gorm:"size:191;not null"`
	SizeBytes                    int64     `gorm:"not null"`
	OriginalURL                  string    `gorm:"size:2048;not null"`
	PreservedURL                 string    `gorm:"size:2048;not null"`
	PreservedMessageDiscordID    string    `gorm:"size:32;not null"`
	PreservedAttachmentDiscordID string    `gorm:"size:32;not null"`
	CopyOutcome                  string    `gorm:"size:32;not null"`
	Warning                      string    `gorm:"type:text;not null"`
}

func (attachmentRecord) TableName() string { return "case_evidence_attachments" }

// caseNotificationRecord is the one member DM a case may send. The unique
// case_id is what makes it one.
type caseNotificationRecord struct {
	ID                       string                   `gorm:"type:char(26);primaryKey"`
	CreatedAt                time.Time                `gorm:"not null"`
	UpdatedAt                time.Time                `gorm:"not null"`
	CaseID                   string                   `gorm:"type:char(26);not null;uniqueIndex"`
	Status                   quack.NotificationStatus `gorm:"size:32;not null;index"`
	PreparedChannelDiscordID string                   `gorm:"size:32;not null;default:''"`
	RenderedMessage          string                   `gorm:"type:text;not null"`
	DeliveryMessageDiscordID string                   `gorm:"size:32;not null;default:''"`
	AttemptCount             uint8                    `gorm:"not null;default:0"`
	LastErrorCode            string                   `gorm:"size:64;not null;default:''"`
	LastError                string                   `gorm:"type:text;not null"`
	LeaseToken               string                   `gorm:"size:64;not null;default:''"`
	LeaseExpiresAt           *time.Time
	SentAt                   *time.Time
}

func (caseNotificationRecord) TableName() string { return "case_notifications" }

// caseEventRecord is one entry in a case's timeline. Visibility decides
// whether the member sees it.
type caseEventRecord struct {
	ID                 string                `gorm:"type:char(26);primaryKey"`
	CreatedAt          time.Time             `gorm:"not null;index:idx_case_events_case_created,priority:2"`
	UpdatedAt          time.Time             `gorm:"not null"`
	CaseID             string                `gorm:"type:char(26);not null;index:idx_case_events_case_created,priority:1"`
	GuildID            string                `gorm:"type:char(26);not null"`
	EventType          quack.CaseEventType   `gorm:"size:64;not null"`
	ActorDiscordUserID string                `gorm:"size:32;not null;default:''"`
	ActorType          string                `gorm:"size:32;not null;default:'system'"`
	Visibility         quack.EventVisibility `gorm:"size:32;not null;default:'staff'"`
	Body               string                `gorm:"type:text;not null"`
	MetadataJSON       string                `gorm:"type:json;not null"`
}

func (caseEventRecord) TableName() string { return "case_events" }

// appealRecord is a member's appeal. The unique case_id allows one appeal per
// case; Version is an optimistic lock for staff decisions.
type appealRecord struct {
	ID                      string             `gorm:"type:char(26);primaryKey"`
	CreatedAt               time.Time          `gorm:"not null;index:idx_appeals_guild_created,priority:2"`
	UpdatedAt               time.Time          `gorm:"not null"`
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_appeals_guild_status,priority:1;index:idx_appeals_guild_target,priority:1;index:idx_appeals_guild_created,priority:1"`
	CaseID                  *string            `gorm:"type:char(26);uniqueIndex"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_appeals_guild_target,priority:2"`
	Status                  quack.AppealStatus `gorm:"size:32;not null;default:'pending';index:idx_appeals_guild_status,priority:2"`
	Content                 string             `gorm:"type:text;not null"`
	QuestionSnapshotJSON    string             `gorm:"type:json;not null"`
	AnswersJSON             string             `gorm:"type:json;not null"`
	Version                 uint64             `gorm:"type:bigint unsigned;not null;default:1"`
	DecisionReason          string             `gorm:"type:text"`
	ReviewedByDiscordUserID string             `gorm:"size:32;not null;default:''"`
	ReviewedAt              *time.Time
	ReviewMessageDiscordID  string `gorm:"size:32;not null;default:''"`
	MetadataJSON            string `gorm:"type:json;not null"`
}

func (appealRecord) TableName() string { return "appeals" }

// appealEventRecord is one entry in an appeal's timeline.
type appealEventRecord struct {
	ID                 string    `gorm:"type:char(26);primaryKey"`
	CreatedAt          time.Time `gorm:"not null"`
	UpdatedAt          time.Time `gorm:"not null"`
	AppealID           string    `gorm:"type:char(26);not null;index"`
	GuildID            string    `gorm:"type:char(26);not null"`
	EventType          string    `gorm:"size:64;not null"`
	ActorDiscordUserID string    `gorm:"size:32;not null;default:''"`
	ActorType          string    `gorm:"size:32;not null"`
	Body               string    `gorm:"type:text;not null"`
	MetadataJSON       string    `gorm:"type:json;not null"`
}

func (appealEventRecord) TableName() string { return "appeal_events" }

// appealSettingsRecord is a guild's custom appeal form. Guilds without one
// use the default questions.
type appealSettingsRecord struct {
	ID                     string    `gorm:"type:char(26);primaryKey"`
	CreatedAt              time.Time `gorm:"not null"`
	UpdatedAt              time.Time `gorm:"not null"`
	GuildID                string    `gorm:"type:char(26);not null;uniqueIndex"`
	QuestionsJSON          string    `gorm:"type:json;not null"`
	UpdatedByDiscordUserID string    `gorm:"size:32;not null"`
}

func (appealSettingsRecord) TableName() string { return "guild_appeal_settings" }

// appealNotificationRecord is an outbox row. The unique event_id means each
// appeal timeline event notifies at most once.
type appealNotificationRecord struct {
	ID                  string                           `gorm:"type:char(26);primaryKey"`
	CreatedAt           time.Time                        `gorm:"not null"`
	UpdatedAt           time.Time                        `gorm:"not null"`
	AppealID            string                           `gorm:"type:char(26);not null;index"`
	EventID             string                           `gorm:"type:char(26);not null;uniqueIndex"`
	GuildID             string                           `gorm:"type:char(26);not null"`
	TargetDiscordUserID string                           `gorm:"size:32;not null"`
	Audience            quack.AppealNotificationAudience `gorm:"size:32;not null"`
	Status              quack.AppealNotificationStatus   `gorm:"size:32;not null;index"`
	Body                string                           `gorm:"type:text;not null"`
	DeliveryMessageID   string                           `gorm:"size:32;not null;default:''"`
	LastErrorCode       string                           `gorm:"size:64;not null;default:''"`
	LeaseToken          string                           `gorm:"size:64;not null;default:''"`
	LeaseExpiresAt      *time.Time
}

func (appealNotificationRecord) TableName() string { return "appeal_notifications" }

// auditRecord is append-only; see audit.go.
type auditRecord struct {
	ID                  string            `gorm:"type:char(26);primaryKey;index:idx_audit_guild_created,priority:3"`
	CreatedAt           time.Time         `gorm:"not null;index:idx_audit_guild_created,priority:2"`
	UpdatedAt           time.Time         `gorm:"not null"`
	GuildID             string            `gorm:"type:char(26);not null;index:idx_audit_guild_created,priority:1;index:idx_audit_guild_action,priority:1"`
	ActorDiscordUserID  string            `gorm:"size:32;not null;default:''"`
	ActorPermissionBits uint64            `gorm:"type:bigint unsigned;not null;default:0"`
	Source              quack.AuditSource `gorm:"size:32;not null"`
	Action              string            `gorm:"size:96;not null;index:idx_audit_guild_action,priority:2"`
	ResourceType        string            `gorm:"size:64;not null;index:idx_audit_resource,priority:1"`
	ResourceID          string            `gorm:"size:64;not null;index:idx_audit_resource,priority:2"`
	Result              quack.AuditResult `gorm:"size:32;not null"`
	FailureReason       string            `gorm:"type:text"`
	CorrelationID       string            `gorm:"size:128;not null;default:''"`
	RequestID           string            `gorm:"size:128;not null;default:''"`
	MetadataJSON        string            `gorm:"type:json;not null"`
}

func (auditRecord) TableName() string { return "audit_log_entries" }

// v4BatchRecord is one imported v4 export file. The unique (guild, source,
// checksum) makes a re-run of the same file a no-op.
type v4BatchRecord struct {
	ID                   string    `gorm:"type:varchar(64);primaryKey"`
	CreatedAt            time.Time `gorm:"not null"`
	GuildID              string    `gorm:"type:char(26);not null;uniqueIndex:idx_v4_batches_source_checksum,priority:1"`
	SourceName           string    `gorm:"size:191;not null;uniqueIndex:idx_v4_batches_source_checksum,priority:2"`
	Checksum             string    `gorm:"type:char(64);not null;uniqueIndex:idx_v4_batches_source_checksum,priority:3"`
	ActorDiscordUserID   string    `gorm:"size:32;not null"`
	RecordCount          int       `gorm:"not null"`
	CreatedCount         int       `gorm:"not null"`
	AlreadyImportedCount int       `gorm:"not null"`
	WarningCount         int       `gorm:"not null"`
}

func (v4BatchRecord) TableName() string { return "v4_import_batches" }

// v4SourceRecord maps one v4 case to the historical case it became. Each v4
// case imports once per guild, and each imported case has one source.
type v4SourceRecord struct {
	ID               string    `gorm:"type:char(26);primaryKey"`
	CreatedAt        time.Time `gorm:"not null"`
	BatchID          string    `gorm:"type:varchar(64);not null;index"`
	GuildID          string    `gorm:"type:char(26);not null;uniqueIndex:idx_v4_sources_identity,priority:1"`
	SourceName       string    `gorm:"size:191;not null;uniqueIndex:idx_v4_sources_identity,priority:2"`
	SourceID         string    `gorm:"size:191;not null;uniqueIndex:idx_v4_sources_identity,priority:3"`
	SourceCaseNumber uint64    `gorm:"type:bigint unsigned;not null"`
	TargetCaseID     string    `gorm:"type:char(26);not null;uniqueIndex"`
	Fingerprint      string    `gorm:"type:char(64);not null"`
}

func (v4SourceRecord) TableName() string { return "v4_import_sources" }

// tables lists every table the baseline creates, the modules' included,
// parents before children, so dropping them in reverse order is always safe.
func tables() []any {
	core := []any{
		&guildRecord{},
		&guildSettingsRecord{},
		&staffMemberRecord{},
		&templateRecord{},
		&contextFieldRecord{},
		&levelRecord{},
		&levelActionRecord{},
		&caseRecord{},
		&executionRecord{},
		&attemptRecord{},
		&evidenceRecord{},
		&attachmentRecord{},
		&caseNotificationRecord{},
		&caseEventRecord{},
		&appealRecord{},
		&appealEventRecord{},
		&appealSettingsRecord{},
		&appealNotificationRecord{},
		&auditRecord{},
		&v4BatchRecord{},
		&v4SourceRecord{},
	}
	core = append(core, modules.Models()...)
	core = append(core, tickets.Models()...)
	return append(core, honeypot.Models()...)
}
