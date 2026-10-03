package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	"github.com/quackdiscord/bot/internal/v4import"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const importGuildID = "01J40000000000000000000001"

func TestV4ImportDryRunIdempotencyIsolationCollisionAndRollback(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate baseline: %v", err)
	}
	if err := migration0400V4HistoricalImport(10).Up(db); err != nil {
		t.Fatalf("apply 0400: %v", err)
	}
	seedImportGuild(t, db)
	fixture, err := os.ReadFile("../v4import/testdata/historical_cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	importer := v4import.New(repositories)

	dry, err := importer.Import(context.Background(), "fixture", importGuildID, "operator", bytes.NewReader(fixture), true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if dry.Created != 0 || len(dry.Decisions) != 4 {
		t.Fatalf("unexpected dry-run report: %+v", dry)
	}
	var count int64
	_ = db.Model(&model.Case{}).Count(&count).Error
	if count != 0 {
		t.Fatalf("dry run wrote %d cases", count)
	}

	report, err := importer.Import(context.Background(), "fixture", importGuildID, "operator", bytes.NewReader(fixture), false)
	if err != nil {
		t.Fatalf("apply import: %v", err)
	}
	if report.Created != 4 {
		t.Fatalf("expected four created cases, got %+v", report)
	}
	var cases []model.Case
	if err := db.Order("case_number").Find(&cases).Error; err != nil {
		t.Fatal(err)
	}
	if len(cases) != 4 {
		t.Fatalf("expected four cases, got %d", len(cases))
	}
	for _, item := range cases {
		if item.Source != model.CaseSourceV4Import || item.TemplateID != nil || !strings.Contains(item.MetadataJSON, `"historical":true`) {
			t.Fatalf("case is not historical-only: %+v", item)
		}
		var actions, notifications int64
		_ = db.Model(&model.CaseActionExecution{}).Where("case_id = ?", item.ID).Count(&actions).Error
		_ = db.Model(&model.CaseNotification{}).Where("case_id = ?", item.ID).Count(&notifications).Error
		if actions != 0 || notifications != 0 {
			t.Fatalf("import created side effects for %s", item.ID)
		}
	}
	member, err := repositories.ListCasesFiltered(context.Background(), model.ListCasesParams{GuildID: importGuildID, TargetDiscordUserID: "member-departed"})
	if err != nil || member.Total != 1 {
		t.Fatalf("member-owned historical projection unavailable: result=%+v err=%v", member, err)
	}
	service := quack.NewCaseService(repositories)
	staffContext := &quack.GuildStaffContext{Guild: &model.Guild{ULIDModel: model.ULIDModel{ID: importGuildID}}, Staff: &model.StaffMember{DiscordUserID: "operator"}, Permissions: map[model.PermissionAction]bool{model.PermissionActionCaseRead: true}}
	staffHistory, err := service.List(context.Background(), staffContext, quack.CaseListInput{})
	if err != nil || staffHistory.Total != 4 {
		t.Fatalf("authorized staff history unavailable: result=%+v err=%v", staffHistory, err)
	}
	memberHistory, err := service.ListMemberCases(context.Background(), importGuildID, "member-departed", quack.CaseListInput{})
	if err != nil || memberHistory.Total != 1 {
		t.Fatalf("authorized member history unavailable: result=%+v err=%v", memberHistory, err)
	}
	if _, err := service.GetMemberCase(context.Background(), memberHistory.Cases[0].ID, "different-member"); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("non-owner read was not concealed: %v", err)
	}

	repeat, err := importer.Import(context.Background(), "fixture", importGuildID, "operator", bytes.NewReader(fixture), false)
	if err != nil {
		t.Fatalf("repeat import: %v", err)
	}
	if repeat.Created != 0 || repeat.AlreadyImported != 4 {
		t.Fatalf("repeat was not idempotent: %+v", repeat)
	}

	collision := bytes.Replace(fixture, []byte("Historical warning"), []byte("Changed warning"), 1)
	_, err = importer.Import(context.Background(), "fixture", importGuildID, "operator", bytes.NewReader(collision), false)
	if !errors.Is(err, v4import.ErrSourceCollision) {
		t.Fatalf("expected source collision, got %v", err)
	}
	if err := importer.Rollback(context.Background(), importGuildID, report.BatchID, "operator"); err != nil {
		t.Fatalf("rollback import: %v", err)
	}
	_ = db.Model(&model.Case{}).Count(&count).Error
	if count != 0 {
		t.Fatalf("rollback left %d cases", count)
	}
}

func TestFinalConstraintsPreserveHistoryAndFlagExpiredActions(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedImportGuild(t, db)
	caseID := "01J40000000000000000000011"
	item := model.Case{ULIDModel: model.ULIDModel{ID: caseID, CreatedAt: now, UpdatedAt: now}, GuildID: importGuildID, CaseNumber: 1, TemplateSnapshotJSON: "{}", TargetDiscordUserID: "member", ModeratorDiscordUserID: "mod", Reason: "preserve", Validity: model.CaseValidityValid, Source: model.CaseSourceDiscord, MetadataJSON: "{}", ContextValuesJSON: "[]"}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-time.Minute)
	execution := model.CaseActionExecution{ULIDModel: model.ULIDModel{ID: "01J40000000000000000000012", CreatedAt: now, UpdatedAt: now}, CaseID: caseID, Position: 0, ActionType: model.ActionTimeoutUser, Status: model.ActionExecutionRunning, IdempotencyKey: "preserved-action", ConfigSnapshotJSON: "{}", LeaseExpiresAt: &expired}
	if err := db.Create(&execution).Error; err != nil {
		t.Fatal(err)
	}
	if err := applyFinalStorageConstraints(db); err != nil {
		t.Fatalf("apply 0410: %v", err)
	}
	var preserved model.Case
	if err := db.First(&preserved, "id = ?", caseID).Error; err != nil || preserved.Reason != "preserve" {
		t.Fatalf("history was not preserved: %+v %v", preserved, err)
	}
	var review ActionManualReviewRecord
	if err := db.First(&review, "execution_id = ?", execution.ID).Error; err != nil || review.Reason != "expired_running_action" {
		t.Fatalf("expired action was not flagged: %+v %v", review, err)
	}
	if err := applyFinalStorageConstraints(db); err != nil {
		t.Fatalf("rerun 0410: %v", err)
	}
	template := CaseTemplateRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000013", CreatedAt: now, UpdatedAt: now}, GuildID: importGuildID, Slug: "constraints", Name: "Constraints", Description: "final", ReasonTemplate: "reason", Appealable: true, Enabled: true, Version: 1, CreatedByDiscordUserID: "mod", UpdatedByDiscordUserID: "mod"}
	if err := db.Create(&template).Error; err != nil {
		t.Fatal(err)
	}
	firstLevel := CaseTemplateLevelRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000014", CreatedAt: now, UpdatedAt: now}, TemplateID: template.ID, Position: 1, Name: "Default", IsDefault: true, Enabled: true}
	if err := db.Create(&firstLevel).Error; err != nil {
		t.Fatal(err)
	}
	secondDefault := CaseTemplateLevelRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000015", CreatedAt: now, UpdatedAt: now}, TemplateID: template.ID, Position: 2, Name: "Also default", IsDefault: true, Enabled: true}
	if err := db.Create(&secondDefault).Error; err == nil {
		t.Fatal("expected one-default-level constraint")
	}
	firstAction := CaseTemplateLevelActionRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000016", CreatedAt: now, UpdatedAt: now}, LevelID: firstLevel.ID, Position: 1, ActionType: model.ActionTimeoutUser, ConfigJSON: "{}", Enabled: true}
	if err := db.Create(&firstAction).Error; err != nil {
		t.Fatal(err)
	}
	secondAction := CaseTemplateLevelActionRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J40000000000000000000017", CreatedAt: now, UpdatedAt: now}, LevelID: firstLevel.ID, Position: 2, ActionType: model.ActionBanUser, ConfigJSON: "{}", Enabled: true}
	if err := db.Create(&secondAction).Error; err == nil {
		t.Fatal("expected one-enforcement-action constraint")
	}
}

func TestMySQLV4ImportFinalConstraintsAndRestoreSafety(t *testing.T) {
	db := openMySQLMigrationDB(t)
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate MySQL baseline: %v", err)
	}
	if err := migration0400V4HistoricalImport(10).Up(db); err != nil {
		t.Fatalf("apply MySQL logical 0400: %v", err)
	}
	seedImportGuild(t, db)
	fixture, err := os.ReadFile("../v4import/testdata/historical_cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	importer := v4import.New(repositories)
	report, err := importer.Import(context.Background(), "mysql-fixture", importGuildID, "operator", bytes.NewReader(fixture), false)
	if err != nil || report.Created != 4 {
		t.Fatalf("import MySQL fixture: report=%+v err=%v", report, err)
	}
	if err := migration0410FinalStorageConstraints(11).Up(db); err != nil {
		t.Fatalf("apply MySQL logical 0410: %v", err)
	}
	repeat, err := importer.Import(context.Background(), "mysql-fixture", importGuildID, "operator", bytes.NewReader(fixture), false)
	if err != nil || repeat.Created != 0 || repeat.AlreadyImported != 4 {
		t.Fatalf("restored duplicate prevention failed: report=%+v err=%v", repeat, err)
	}
	if err := importer.Rollback(context.Background(), importGuildID, report.BatchID, "operator"); err != nil {
		t.Fatalf("prepare concurrent import: %v", err)
	}
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, concurrentErr := importer.Import(context.Background(), "mysql-fixture", importGuildID, "operator", bytes.NewReader(fixture), false)
			errs <- concurrentErr
		}()
	}
	wait.Wait()
	close(errs)
	for concurrentErr := range errs {
		if concurrentErr != nil {
			t.Fatalf("concurrent import failed: %v", concurrentErr)
		}
	}
	var caseCount, sourceCount, batchCount int64
	_ = db.Model(&model.Case{}).Where("guild_id = ? AND source = ?", importGuildID, model.CaseSourceV4Import).Count(&caseCount).Error
	_ = db.Model(&V4ImportSourceRecord{}).Where("guild_id = ?", importGuildID).Count(&sourceCount).Error
	_ = db.Model(&V4ImportBatchRecord{}).Where("guild_id = ?", importGuildID).Count(&batchCount).Error
	if caseCount != 4 || sourceCount != 4 || batchCount != 1 {
		t.Fatalf("concurrent import duplicated state: cases=%d sources=%d batches=%d", caseCount, sourceCount, batchCount)
	}
}

func seedImportGuild(t *testing.T, db *gorm.DB) {
	t.Helper()
	now := time.Now().UTC()
	guild := model.Guild{ULIDModel: model.ULIDModel{ID: importGuildID, CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "discord-import-guild", Name: "Import guild", OwnerDiscordUserID: "owner", IsActive: true}
	if err := db.Create(&guild).Error; err != nil {
		t.Fatal(err)
	}
}
