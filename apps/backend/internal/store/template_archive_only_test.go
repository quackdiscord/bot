package store

import (
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLiveTemplateRecordDoesNotEnableSoftDeleteSemantics(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	guild := quack.Guild{ULIDModel: quack.ULIDModel{ID: "01J40000000000000000000021", CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "archive-only-guild", Name: "Archive only", OwnerDiscordUserID: "owner", IsActive: true}
	if err := db.Create(&guild).Error; err != nil {
		t.Fatal(err)
	}
	record := CaseTemplateRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000022", CreatedAt: now, UpdatedAt: now}, GuildID: guild.ID, Slug: "legacy", Name: "Legacy", Description: "preserved", ReasonTemplate: "reason", Appealable: true, Enabled: true, Version: 1, CreatedByDiscordUserID: "operator", UpdatedByDiscordUserID: "operator"}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE case_templates SET deleted_at = ? WHERE id = ?", now, record.ID).Error; err != nil {
		t.Fatal(err)
	}
	var live CaseTemplateRecord
	if err := db.First(&live, "id = ?", record.ID).Error; err != nil {
		t.Fatalf("compatibility column hid live template: %v", err)
	}
	if err := applyFinalStorageConstraints(db); err != nil {
		t.Fatal(err)
	}
	live = CaseTemplateRecord{}
	if err := db.First(&live, "id = ?", record.ID).Error; err != nil {
		t.Fatal(err)
	}
	if live.DeletedAt != nil || live.ArchivedAt == nil {
		t.Fatalf("legacy deletion was not converted to archive: %+v", live)
	}
}
