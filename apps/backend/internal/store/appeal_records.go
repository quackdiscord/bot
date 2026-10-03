package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// appealV5Record is the logical 0200 live persistence shape layered over the preserved placeholder table.
type appealV5Record struct {
	ULIDModelRecord
	GuildID                 string             `gorm:"type:char(26);not null;index:idx_appeal_guild_status,priority:1;index:idx_appeal_guild_user,priority:1"`
	CaseID                  *string            `gorm:"type:char(26);uniqueIndex"`
	TargetDiscordUserID     string             `gorm:"size:32;not null;index:idx_appeal_guild_user,priority:2"`
	Status                  quack.AppealStatus `gorm:"size:32;not null;default:'pending';index:idx_appeal_guild_status,priority:2"`
	Content                 string             `gorm:"type:text;not null"`
	QuestionSnapshotJSON    string             `gorm:"type:json;not null"`
	AnswersJSON             string             `gorm:"type:json;not null"`
	Version                 uint64             `gorm:"type:bigint unsigned;not null;default:1"`
	DecisionReason          string             `gorm:"type:text"`
	ReviewedByDiscordUserID string             `gorm:"size:32"`
	ReviewedAt              *time.Time         `gorm:"index"`
	ReviewMessageDiscordID  string             `gorm:"size:32"`
	MetadataJSON            string             `gorm:"type:json;not null"`
}

// TableName preserves the appeal table while logical migration 0200 extends it.
func (appealV5Record) TableName() string { return "appeals" }

// appealEventV5Record is the logical 0200 immutable timeline shape.
type appealEventV5Record struct {
	ULIDModelRecord
	AppealID           string `gorm:"type:char(26);not null;index"`
	GuildID            string `gorm:"type:char(26);not null;index"`
	EventType          string `gorm:"size:64;not null;index"`
	ActorDiscordUserID string `gorm:"size:32;index"`
	ActorType          string `gorm:"size:32;not null"`
	Body               string `gorm:"type:text;not null"`
	MetadataJSON       string `gorm:"type:json;not null"`
}

// TableName preserves the appeal event table while logical migration 0200 extends it.
func (appealEventV5Record) TableName() string { return "appeal_events" }

func appealRecord(item quack.Appeal) *appealV5Record {
	return &appealV5Record{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, GuildID: item.GuildID, CaseID: item.CaseID, TargetDiscordUserID: item.TargetDiscordUserID, Status: item.Status, Content: item.Content, QuestionSnapshotJSON: item.QuestionSnapshotJSON, AnswersJSON: item.AnswersJSON, Version: item.Version, DecisionReason: item.DecisionReason, ReviewedByDiscordUserID: item.ReviewedByDiscordUserID, ReviewedAt: item.ReviewedAt, ReviewMessageDiscordID: item.ReviewMessageDiscordID, MetadataJSON: item.MetadataJSON}
}

func appealModel(record appealV5Record) *quack.Appeal {
	return &quack.Appeal{ULIDModel: quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, GuildID: record.GuildID, CaseID: record.CaseID, TargetDiscordUserID: record.TargetDiscordUserID, Status: record.Status, Content: record.Content, QuestionSnapshotJSON: record.QuestionSnapshotJSON, AnswersJSON: record.AnswersJSON, Version: record.Version, DecisionReason: record.DecisionReason, ReviewedByDiscordUserID: record.ReviewedByDiscordUserID, ReviewedAt: record.ReviewedAt, ReviewMessageDiscordID: record.ReviewMessageDiscordID, MetadataJSON: record.MetadataJSON}
}

func appealEventRecord(item quack.AppealEvent) *appealEventV5Record {
	return &appealEventV5Record{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, AppealID: item.AppealID, GuildID: item.GuildID, EventType: item.EventType, ActorDiscordUserID: item.ActorDiscordUserID, ActorType: item.ActorType, Body: item.Body, MetadataJSON: item.MetadataJSON}
}

func appealEventModel(record appealEventV5Record) quack.AppealEvent {
	return quack.AppealEvent{ULIDModel: quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, AppealID: record.AppealID, GuildID: record.GuildID, EventType: record.EventType, ActorDiscordUserID: record.ActorDiscordUserID, ActorType: record.ActorType, Body: record.Body, MetadataJSON: record.MetadataJSON}
}

func guildAppealSettingsRecord(item quack.GuildAppealSettings) GuildAppealSettingsRecord {
	return GuildAppealSettingsRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, GuildID: item.GuildID, QuestionsJSON: item.QuestionsJSON, UpdatedByDiscordUserID: item.UpdatedByDiscordUserID}
}

func guildAppealSettingsModel(record GuildAppealSettingsRecord) *quack.GuildAppealSettings {
	return &quack.GuildAppealSettings{ULIDModel: quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, GuildID: record.GuildID, QuestionsJSON: record.QuestionsJSON, UpdatedByDiscordUserID: record.UpdatedByDiscordUserID}
}

func appealNotificationRecord(item quack.AppealNotification) *AppealNotificationRecord {
	return &AppealNotificationRecord{ULIDModelRecord: ULIDModelRecord{ID: item.ID, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}, AppealID: item.AppealID, EventID: item.EventID, GuildID: item.GuildID, TargetDiscordUserID: item.TargetDiscordUserID, Audience: item.Audience, Status: item.Status, Body: item.Body, DeliveryMessageID: item.DeliveryMessageID, LastErrorCode: item.LastErrorCode, LeaseToken: item.LeaseToken, LeaseExpiresAt: item.LeaseExpiresAt}
}

func appealNotificationModel(record AppealNotificationRecord) quack.AppealNotification {
	return quack.AppealNotification{ULIDModel: quack.ULIDModel{ID: record.ID, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt}, AppealID: record.AppealID, EventID: record.EventID, GuildID: record.GuildID, TargetDiscordUserID: record.TargetDiscordUserID, Audience: record.Audience, Status: record.Status, Body: record.Body, DeliveryMessageID: record.DeliveryMessageID, LastErrorCode: record.LastErrorCode, LeaseToken: record.LeaseToken, LeaseExpiresAt: record.LeaseExpiresAt}
}
