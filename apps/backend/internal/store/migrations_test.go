package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// openSQLiteMigrationDB creates an isolated SQLite database for migration tests.
func openSQLiteMigrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:migration-%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite migration database: %v", err)
	}
	return db
}

func TestMigrateSQLiteForwardAndRerun(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	repositories := New(db, nil)

	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate clean sqlite schema: %v", err)
	}
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("rerun sqlite migrations: %v", err)
	}

	for _, table := range []string{"guilds", "cases", "case_action_attempts", "case_events", "audit_log_entries", "quack_schema_migrations"} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s", table)
		}
	}
	var ledgerCount int64
	if err := db.Model(&schemaMigration{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if ledgerCount != int64(len(registeredMigrations())) {
		t.Fatalf("expected %d migration ledger rows after rerun, got %d", len(registeredMigrations()), ledgerCount)
	}
}

func TestMigration0002PreservesAndQuarantinesIncompatibleTemplates(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	through0002 := []migration{migration0001InitialV5Schema(), migration0002SimplifyTemplateModel()}
	if err := runMigrations(db, []migration{migration0001InitialV5Schema()}); err != nil {
		t.Fatalf("apply frozen baseline: %v", err)
	}
	want := insertRepresentativeHistory(t, db)
	validID := insertMigration0002Template(t, db, "valid-template", true, 0)
	disabledID := insertMigration0002Template(t, db, "disabled-template", false, 0)
	windowID := insertMigration0002Template(t, db, "window-template", true, 60)
	softDeletedID := insertMigration0002Template(t, db, "deleted-template", true, 0)
	deletedAt := time.Now().UTC().Add(-time.Hour)
	if err := db.Model(&CaseTemplateRecord{}).Where("id = ?", softDeletedID).UpdateColumn("deleted_at", deletedAt).Error; err != nil {
		t.Fatalf("soft delete compatibility fixture: %v", err)
	}

	if err := runMigrations(db, through0002); err != nil {
		t.Fatalf("apply template compatibility migration: %v", err)
	}
	assertTemplateArchiveState(t, db, validID, false)
	assertTemplateArchiveState(t, db, disabledID, true)
	assertTemplateArchiveState(t, db, windowID, true)
	assertTemplateArchiveState(t, db, softDeletedID, true)
	assertTemplateDeletedState(t, db, softDeletedID, false)
	assertRepresentativeHistory(t, db, want)
	var actionRecords []CaseTemplateLevelActionRecord
	if err := db.Order("id ASC").Find(&actionRecords).Error; err != nil {
		t.Fatalf("list preserved configured actions: %v", err)
	}
	if len(actionRecords) != 4 {
		t.Fatalf("expected all four configured actions preserved, got %d", len(actionRecords))
	}
	for _, action := range actionRecords {
		if action.ConfigJSON != `{"duration_minutes":60}` {
			t.Fatalf("configured action %s was rewritten: %s", action.ID, action.ConfigJSON)
		}
	}

	var compatibilityCount int64
	if err := db.Model(&migration0002TemplateCompatibility{}).Count(&compatibilityCount).Error; err != nil {
		t.Fatalf("count compatibility records: %v", err)
	}
	if compatibilityCount != 3 {
		t.Fatalf("expected three quarantined templates, got %d", compatibilityCount)
	}
	if err := runMigrations(db, through0002); err != nil {
		t.Fatalf("rerun template compatibility migration: %v", err)
	}
	if err := db.Model(&migration0002TemplateCompatibility{}).Count(&compatibilityCount).Error; err != nil {
		t.Fatalf("recount compatibility records: %v", err)
	}
	if compatibilityCount != 3 {
		t.Fatalf("expected idempotent compatibility records, got %d", compatibilityCount)
	}

	if err := rollbackLastMigration(db, through0002); err != nil {
		t.Fatalf("roll back template compatibility migration: %v", err)
	}
	assertTemplateArchiveState(t, db, disabledID, false)
	assertTemplateArchiveState(t, db, windowID, false)
	assertTemplateArchiveState(t, db, softDeletedID, false)
	assertTemplateDeletedState(t, db, softDeletedID, true)
	assertRepresentativeHistory(t, db, want)
	if err := runMigrations(db, through0002); err != nil {
		t.Fatalf("reapply template compatibility migration: %v", err)
	}
	assertTemplateArchiveState(t, db, disabledID, true)
	assertTemplateArchiveState(t, db, windowID, true)
	assertTemplateArchiveState(t, db, softDeletedID, true)
	assertTemplateDeletedState(t, db, softDeletedID, false)
}

func TestMigration0002QuarantinedPolicyCannotCrossLiveReadBoundary(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := runMigrations(db, []migration{migration0001InitialV5Schema()}); err != nil {
		t.Fatalf("apply frozen baseline: %v", err)
	}

	invalidID := insertMigration0002Template(t, db, "invalid-policy", true, 0)
	validArchivedID := insertMigration0002Template(t, db, "valid-archived", true, 0)
	archivedAt := time.Now().UTC().Add(-time.Hour)
	if err := db.Model(&CaseTemplateRecord{}).Where("id IN ?", []string{invalidID, validArchivedID}).UpdateColumn("archived_at", archivedAt).Error; err != nil {
		t.Fatalf("archive compatibility fixtures: %v", err)
	}

	var invalidLevel CaseTemplateLevelRecord
	if err := db.Where("template_id = ?", invalidID).First(&invalidLevel).Error; err != nil {
		t.Fatalf("load invalid template level: %v", err)
	}
	if err := db.Model(&CaseTemplateLevelRecord{}).Where("id = ?", invalidLevel.ID).UpdateColumn("is_default", false).Error; err != nil {
		t.Fatalf("remove invalid template default: %v", err)
	}
	secondAction := CaseTemplateLevelActionRecord{
		ULIDModelRecord:  ULIDModelRecord{ID: "second-invalid-action00000", CreatedAt: archivedAt, UpdatedAt: archivedAt},
		LevelID:          invalidLevel.ID,
		Position:         2,
		ActionType:       "kick_user",
		ConfigJSON:       `{}`,
		IdempotencyScope: "case",
		Enabled:          true,
	}
	if err := db.Select("*").Create(&secondAction).Error; err != nil {
		t.Fatalf("create preserved second action: %v", err)
	}

	migrations := registeredMigrations()
	if err := runMigrations(db, migrations[:len(migrations)-1]); err != nil {
		t.Fatalf("apply template compatibility migration: %v", err)
	}

	repositories := New(db, nil)
	got, err := repositories.GetCaseTemplateExpanded(context.Background(), "00000000000000000000000001", invalidID)
	if got != nil {
		t.Fatalf("quarantined policy crossed live read boundary: %+v", got)
	}
	if !errors.Is(err, quack.ErrTemplateCompatibilityReviewRequired) {
		t.Fatalf("expected compatibility review error, got %v", err)
	}
	var compatibilityError *quack.TemplateCompatibilityReviewError
	if !errors.As(err, &compatibilityError) {
		t.Fatalf("expected typed compatibility error, got %T", err)
	}
	if compatibilityError.TemplateID != invalidID || !strings.Contains(compatibilityError.Reason, "level has multiple actions") || !strings.Contains(compatibilityError.Reason, "template does not have exactly one default level") {
		t.Fatalf("expected explicit policy defects, got %+v", compatibilityError)
	}

	var preservedLevels, preservedActions int64
	if err := db.Model(&CaseTemplateLevelRecord{}).Where("template_id = ?", invalidID).Count(&preservedLevels).Error; err != nil {
		t.Fatalf("count preserved levels: %v", err)
	}
	if err := db.Model(&CaseTemplateLevelActionRecord{}).Where("level_id = ?", invalidLevel.ID).Count(&preservedActions).Error; err != nil {
		t.Fatalf("count preserved actions: %v", err)
	}
	if preservedLevels != 1 || preservedActions != 2 {
		t.Fatalf("migration rewrote quarantined policy: levels=%d actions=%d", preservedLevels, preservedActions)
	}

	validArchived, err := repositories.GetCaseTemplateExpanded(context.Background(), "00000000000000000000000001", validArchivedID)
	if err != nil {
		t.Fatalf("get valid archived template: %v", err)
	}
	if validArchived == nil || validArchived.Template.ID != validArchivedID || validArchived.Template.ArchivedAt == nil {
		t.Fatalf("expected readable valid archived template, got %+v", validArchived)
	}
}

func TestMigration0003MapsCasesInventoriesRetiredEventsAndRollsBackExactly(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	through0002 := []migration{migration0001InitialV5Schema(), migration0002SimplifyTemplateModel()}
	if err := runMigrations(db, through0002); err != nil {
		t.Fatalf("apply migrations through 0002: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	guild := GuildRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J30000000000000000000001", CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "migration-0003", Name: "Migration 0003", OwnerDiscordUserID: "owner"}
	if err := db.Create(&guild).Error; err != nil {
		t.Fatalf("create migration guild: %v", err)
	}

	fixtures := []struct {
		id, status, source, wantValidity, wantSource string
	}{
		{"01J30000000000000000000002", "open", "api", "valid", "dashboard"},
		{"01J30000000000000000000003", "action_running", "discord_command", "valid", "discord"},
		{"01J30000000000000000000004", "completed", "automation", "valid", "honeypot"},
		{"01J30000000000000000000005", "failed", "import", "valid", "v4_import"},
		{"01J30000000000000000000006", "appealed", "api", "valid", "dashboard"},
		{"01J30000000000000000000007", "voided", "import", "voided", "v4_import"},
	}
	for index, fixture := range fixtures {
		row := migration0003HistoryCase{
			ID: fixture.id, CreatedAt: now, UpdatedAt: now, GuildID: guild.ID, CaseNumber: uint64(index + 1),
			TemplateVersion: 1, TemplateSnapshotJSON: `{"snapshot":"preserved"}`, TargetDiscordUserID: "target",
			ModeratorDiscordUserID: "moderator", Reason: "immutable reason", Severity: "critical", Weight: 99,
			Status: fixture.status, Source: fixture.source, MetadataJSON: `{"legacy":true}`,
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create case fixture %s: %v", fixture.id, err)
		}
	}

	legacyEventID := "01J30000000000000000000008"
	editedAt := now.Add(time.Minute)
	deletedAt := now.Add(2 * time.Minute)
	if err := db.Exec(`INSERT INTO case_events
		(id, created_at, updated_at, case_id, guild_id, event_type, actor_discord_user_id, actor_type, visibility, body, metadata_json, edited_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		legacyEventID, now, now, fixtures[0].id, guild.ID, "note_added", "moderator", "staff", "internal",
		"private preserved body", `{"bytes":"preserved"}`, editedAt, deletedAt).Error; err != nil {
		t.Fatalf("insert preserved retired event: %v", err)
	}
	for index, eventType := range []string{"note_edited", "note_deleted", "status_changed", "case_created"} {
		id := fmt.Sprintf("01J3000000000000000000001%d", index)
		if err := db.Exec(`INSERT INTO case_events
			(id, created_at, updated_at, case_id, guild_id, event_type, actor_type, visibility, body, metadata_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, id, now, now, fixtures[0].id, guild.ID, eventType, "system", "staff", eventType, `{}`).Error; err != nil {
			t.Fatalf("insert event %s: %v", eventType, err)
		}
	}
	var eventBefore migration0003LegacyEvent
	if err := db.First(&eventBefore, "id = ?", legacyEventID).Error; err != nil {
		t.Fatalf("load retired event before migration: %v", err)
	}

	through0003 := registeredMigrations()[:3]
	if err := runMigrations(db, through0003); err != nil {
		t.Fatalf("apply migration 0003: %v", err)
	}
	if err := runMigrations(db, through0003); err != nil {
		t.Fatalf("rerun migration 0003: %v", err)
	}
	for _, fixture := range fixtures {
		var got migration0003Case
		if err := db.First(&got, "id = ?", fixture.id).Error; err != nil {
			t.Fatalf("load mapped case %s: %v", fixture.id, err)
		}
		if got.Status != fixture.wantValidity || got.Source != fixture.wantSource {
			t.Fatalf("case %s mapped to status=%q source=%q, want %q/%q", fixture.id, got.Status, got.Source, fixture.wantValidity, fixture.wantSource)
		}
	}
	var compatibility migration0003CaseCompatibility
	if err := db.First(&compatibility, "case_id = ?", fixtures[0].id).Error; err != nil {
		t.Fatalf("load case compatibility inventory: %v", err)
	}
	if compatibility.PreviousStatus != "open" || compatibility.PreviousSource != "api" || compatibility.NoteEventCount != 3 || compatibility.StatusEventCount != 1 {
		t.Fatalf("unexpected compatibility inventory: %+v", compatibility)
	}
	var compatibilityCount int64
	if err := db.Model(&migration0003CaseCompatibility{}).Count(&compatibilityCount).Error; err != nil || compatibilityCount != int64(len(fixtures)) {
		t.Fatalf("expected one idempotent compatibility row per case, count=%d err=%v", compatibilityCount, err)
	}
	var eventAfter migration0003LegacyEvent
	if err := db.First(&eventAfter, "id = ?", legacyEventID).Error; err != nil {
		t.Fatalf("load retired event after migration: %v", err)
	}
	assertMigration0003LegacyEventEqual(t, eventAfter, eventBefore)
	liveEvents, err := New(db, nil).ListCaseEvents(context.Background(), fixtures[0].id)
	if err != nil {
		t.Fatalf("list live case events: %v", err)
	}
	if len(liveEvents) != 1 || liveEvents[0].EventType != quack.CaseEventCreated {
		t.Fatalf("retired events crossed live boundary: %+v", liveEvents)
	}

	if err := rollbackLastMigration(db, through0003); err != nil {
		t.Fatalf("roll back migration 0003: %v", err)
	}
	if db.Migrator().HasTable(&migration0003CaseCompatibility{}) {
		t.Fatal("migration-owned compatibility table remained after rollback")
	}
	for _, fixture := range fixtures {
		var got migration0003Case
		if err := db.First(&got, "id = ?", fixture.id).Error; err != nil {
			t.Fatalf("load rolled-back case %s: %v", fixture.id, err)
		}
		if got.Status != fixture.status || got.Source != fixture.source {
			t.Fatalf("case %s rollback restored %q/%q, want %q/%q", fixture.id, got.Status, got.Source, fixture.status, fixture.source)
		}
	}
	var eventRolledBack migration0003LegacyEvent
	if err := db.First(&eventRolledBack, "id = ?", legacyEventID).Error; err != nil {
		t.Fatalf("load retired event after rollback: %v", err)
	}
	assertMigration0003LegacyEventEqual(t, eventRolledBack, eventBefore)
}

func TestMigration0003RejectsUnknownValuesWithoutMutation(t *testing.T) {
	for _, test := range []struct {
		name, status, source string
	}{
		{name: "status", status: "mystery", source: "api"},
		{name: "source", status: "open", source: "mystery"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := openSQLiteMigrationDB(t)
			through0002 := []migration{migration0001InitialV5Schema(), migration0002SimplifyTemplateModel()}
			if err := runMigrations(db, through0002); err != nil {
				t.Fatalf("apply migrations through 0002: %v", err)
			}
			now := time.Now().UTC()
			guild := GuildRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J31000000000000000000001", CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "unknown-" + test.name, Name: "Unknown", OwnerDiscordUserID: "owner"}
			if err := db.Create(&guild).Error; err != nil {
				t.Fatalf("create guild: %v", err)
			}
			row := migration0003HistoryCase{ID: "01J31000000000000000000002", CreatedAt: now, UpdatedAt: now, GuildID: guild.ID, CaseNumber: 1, TemplateVersion: 1, TemplateSnapshotJSON: `{}`, TargetDiscordUserID: "target", ModeratorDiscordUserID: "moderator", Reason: "reason", Severity: "medium", Weight: 1, Status: test.status, Source: test.source, MetadataJSON: `{}`}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("create unknown fixture: %v", err)
			}
			if err := runMigrations(db, registeredMigrations()); err == nil || !strings.Contains(err.Error(), "unknown legacy "+test.name) {
				t.Fatalf("expected explicit unknown %s failure, got %v", test.name, err)
			}
			var got migration0003Case
			if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
				t.Fatalf("load rejected fixture: %v", err)
			}
			if got.Status != test.status || got.Source != test.source || db.Migrator().HasTable(&migration0003CaseCompatibility{}) {
				t.Fatalf("rejected migration mutated state: case=%+v table=%v", got, db.Migrator().HasTable(&migration0003CaseCompatibility{}))
			}
		})
	}
}

func TestMigration0003RollbackDowngradesPostMigrationCases(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	through0003 := registeredMigrations()[:3]
	if err := runMigrations(db, through0003); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	now := time.Now().UTC()
	guild := GuildRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J32000000000000000000001", CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "rollback-guard", Name: "Rollback Guard", OwnerDiscordUserID: "owner"}
	if err := db.Create(&guild).Error; err != nil {
		t.Fatalf("create guild: %v", err)
	}
	postMigration := quack.Case{
		ULIDModel: quack.ULIDModel{ID: "01J32000000000000000000002", CreatedAt: now, UpdatedAt: now},
		GuildID:   guild.ID, CaseNumber: 1, TemplateVersion: 1, TemplateSnapshotJSON: `{}`,
		TargetDiscordUserID: "target", ModeratorDiscordUserID: "moderator", Reason: "reason",
		Validity: quack.CaseValidityValid, Source: quack.CaseSourceDashboard, MetadataJSON: `{}`,
	}
	if err := db.Omit("ContextValuesJSON", "VoidedReason", "VoidedByDiscordUserID", "VoidedAt", "ReplacementCaseID", "ReplacesCaseID", "IdempotencyKey").Create(&postMigration).Error; err != nil {
		t.Fatalf("create post-migration case: %v", err)
	}

	if err := rollbackLastMigration(db, through0003); err != nil {
		t.Fatalf("roll back with post-migration case: %v", err)
	}
	var persisted migration0003Case
	if err := db.First(&persisted, "id = ?", postMigration.ID).Error; err != nil {
		t.Fatalf("load post-migration case: %v", err)
	}
	if persisted.Status != "open" || persisted.Source != "api" {
		t.Fatalf("post-migration case was not downgraded compatibly: %+v", persisted)
	}
	if db.Migrator().HasTable(&migration0003CaseCompatibility{}) {
		t.Fatal("rollback retained migration-owned compatibility bookkeeping")
	}
	var ledgerCount int64
	if err := db.Model(&schemaMigration{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count migration ledger: %v", err)
	}
	if ledgerCount != 2 {
		t.Fatalf("expected migration 0003 ledger row removed, got %d rows", ledgerCount)
	}
}

func TestMigration0003CanonicalRollbackMappings(t *testing.T) {
	for value, want := range map[string]string{"valid": "open", "voided": "voided"} {
		got, ok := migration0003LegacyStatus(value)
		if !ok || got != want {
			t.Fatalf("canonical validity %q mapped to %q/%v, want %q/true", value, got, ok, want)
		}
	}
	for value, want := range map[string]string{
		"dashboard": "api", "discord": "discord_command", "honeypot": "automation", "v4_import": "import",
	} {
		got, ok := migration0003LegacySource(value)
		if !ok || got != want {
			t.Fatalf("canonical source %q mapped to %q/%v, want %q/true", value, got, ok, want)
		}
	}
	if _, ok := migration0003LegacyStatus("unknown"); ok {
		t.Fatal("unknown canonical validity was accepted")
	}
	if _, ok := migration0003LegacySource("unknown"); ok {
		t.Fatal("unknown canonical source was accepted")
	}
}

func TestMigration0002ActionCompatibility(t *testing.T) {
	tests := []struct {
		name   string
		action migration0002Action
		valid  bool
	}{
		{name: "legacy timeout", action: migration0002Action{ActionType: "timeout_user", ConfigJSON: `{"duration_minutes":60}`, Enabled: true, IdempotencyScope: "case"}, valid: true},
		{name: "canonical timeout", action: migration0002Action{ActionType: "timeout_user", ConfigJSON: `{"duration_seconds":3600}`, Enabled: true, IdempotencyScope: "case"}, valid: true},
		{name: "kick", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`, Enabled: true, IdempotencyScope: "case"}, valid: true},
		{name: "maximum ban history", action: migration0002Action{ActionType: "ban_user", ConfigJSON: `{"delete_message_seconds":604800}`, Enabled: true, IdempotencyScope: "case"}, valid: true},
		{name: "multiple config fields", action: migration0002Action{ActionType: "timeout_user", ConfigJSON: `{"duration_seconds":60,"extra":true}`, Enabled: true}, valid: false},
		{name: "action notification", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`, Enabled: true, NotifyUser: true}, valid: false},
		{name: "continuation", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`, Enabled: true, ContinueOnError: true}, valid: false},
		{name: "public backoff", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`, Enabled: true, RetryBackoffMS: 1}, valid: false},
		{name: "disabled", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`}, valid: false},
		{name: "retry limit", action: migration0002Action{ActionType: "kick_user", ConfigJSON: `{}`, Enabled: true, MaxRetries: 11}, valid: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reasons := migration0002ActionReasons(tt.action)
			if tt.valid && len(reasons) != 0 {
				t.Fatalf("expected compatible action, got reasons %v", reasons)
			}
			if !tt.valid && len(reasons) == 0 {
				t.Fatal("expected incompatible action reason")
			}
		})
	}
}

func insertMigration0002Template(t *testing.T, db *gorm.DB, id string, enabled bool, windowMinutes int) string {
	t.Helper()
	now := time.Now().UTC()
	templateID := id + strings.Repeat("0", 26-len(id))
	levelPrefix := "level-" + id
	levelID := levelPrefix + strings.Repeat("0", 26-len(levelPrefix))
	template := CaseTemplateRecord{
		ULIDModelRecord:        ULIDModelRecord{ID: templateID, CreatedAt: now, UpdatedAt: now},
		GuildID:                "00000000000000000000000001",
		Slug:                   id,
		Name:                   id,
		Description:            "compatibility fixture",
		ReasonTemplate:         "fixture reason",
		DefaultSeverity:        "medium",
		Enabled:                enabled,
		Version:                1,
		CreatedByDiscordUserID: "moderator",
		UpdatedByDiscordUserID: "moderator",
	}
	if err := db.Select("*").Create(&template).Error; err != nil {
		t.Fatalf("create template fixture %s: %v", id, err)
	}
	if !enabled {
		if err := db.Model(&CaseTemplateRecord{}).Where("id = ?", templateID).UpdateColumn("enabled", false).Error; err != nil {
			t.Fatalf("disable template fixture %s: %v", id, err)
		}
	}
	level := CaseTemplateLevelRecord{
		ULIDModelRecord: ULIDModelRecord{ID: levelID, CreatedAt: now, UpdatedAt: now},
		TemplateID:      templateID,
		Position:        1,
		Name:            "Default",
		IsDefault:       true,
		WindowMinutes:   windowMinutes,
		Enabled:         true,
	}
	if err := db.Select("*").Create(&level).Error; err != nil {
		t.Fatalf("create level fixture %s: %v", id, err)
	}
	actionPrefix := "action-" + id
	action := CaseTemplateLevelActionRecord{
		ULIDModelRecord:  ULIDModelRecord{ID: actionPrefix + strings.Repeat("0", 26-len(actionPrefix)), CreatedAt: now, UpdatedAt: now},
		LevelID:          levelID,
		Position:         1,
		ActionType:       "timeout_user",
		ConfigJSON:       `{"duration_minutes":60}`,
		IdempotencyScope: "case",
		Enabled:          true,
	}
	if err := db.Select("*").Create(&action).Error; err != nil {
		t.Fatalf("create action fixture %s: %v", id, err)
	}
	return templateID
}

func assertTemplateArchiveState(t *testing.T, db *gorm.DB, templateID string, archived bool) {
	t.Helper()
	var record CaseTemplateRecord
	if err := db.Unscoped().Where("id = ?", templateID).First(&record).Error; err != nil {
		t.Fatalf("load template %s: %v", templateID, err)
	}
	if (record.ArchivedAt != nil) != archived {
		t.Fatalf("template %s archived=%v, want %v", templateID, record.ArchivedAt != nil, archived)
	}
}

func assertTemplateDeletedState(t *testing.T, db *gorm.DB, templateID string, deleted bool) {
	t.Helper()
	var record CaseTemplateRecord
	if err := db.Unscoped().Where("id = ?", templateID).First(&record).Error; err != nil {
		t.Fatalf("load template %s: %v", templateID, err)
	}
	if (record.DeletedAt != nil) != deleted {
		t.Fatalf("template %s deleted=%v, want %v", templateID, record.DeletedAt != nil, deleted)
	}
}

func TestRegisteredMigrationsProduceEveryCurrentSchemaFieldAndIndex(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := New(db, nil).Migrate(); err != nil {
		t.Fatalf("migrate clean sqlite schema: %v", err)
	}

	for _, currentModel := range schemaModels() {
		statement := &gorm.Statement{DB: db}
		if err := statement.Parse(currentModel); err != nil {
			t.Fatalf("parse current schema model %T: %v", currentModel, err)
		}
		if !db.Migrator().HasTable(currentModel) {
			t.Fatalf("registered migrations did not create %s", statement.Schema.Table)
		}
		for _, field := range statement.Schema.Fields {
			if field.DBName != "" && !db.Migrator().HasColumn(currentModel, field.DBName) {
				t.Fatalf("registered migrations did not create %s.%s", statement.Schema.Table, field.DBName)
			}
		}
		for _, index := range statement.Schema.ParseIndexes() {
			if !db.Migrator().HasIndex(currentModel, index.Name) {
				t.Fatalf("registered migrations did not create index %s on %s", index.Name, statement.Schema.Table)
			}
		}
	}
}

func TestMigration0001SourceDoesNotDependOnLiveDomainModels(t *testing.T) {
	if strings.Contains(migration0001Source, `internal/quack"`) {
		t.Fatal("frozen migration source must use primitive storage types, not live domain aliases")
	}
}

func TestMigrateAdoptsCurrentSchemaWithoutLosingHistory(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := applyInitialV5Schema(db); err != nil {
		t.Fatalf("create representative pre-ledger schema: %v", err)
	}
	want := insertRepresentativeHistory(t, db)

	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("adopt representative schema: %v", err)
	}
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("rerun representative schema migration: %v", err)
	}
	assertRepresentativeHistory(t, db, want)
}

func TestMigrateAddsKnownCurrentV5ColumnsToOlderSchema(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	if err := db.Exec(`CREATE TABLE schema_migrations (name text primary key, applied_at datetime not null)`).Error; err != nil {
		t.Fatalf("create obsolete migration table: %v", err)
	}
	if err := db.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES ("0001_v5_schema", CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert obsolete migration row: %v", err)
	}
	if err := db.Exec(`CREATE TABLE case_template_levels (
		id text primary key,
		created_at datetime not null,
		updated_at datetime not null,
		template_id text not null,
		position integer not null,
		name text not null,
		is_default numeric not null,
		trigger_case_count integer not null,
		window_minutes integer not null,
		enabled numeric not null
	)`).Error; err != nil {
		t.Fatalf("create older case_template_levels: %v", err)
	}

	if err := New(db, nil).Migrate(); err != nil {
		t.Fatalf("migrate older schema: %v", err)
	}
	if !db.Migrator().HasColumn("case_template_levels", "notify_user") {
		t.Fatal("expected notify_user column to be added")
	}
	if !db.Migrator().HasColumn("case_template_levels", "notification_type") {
		t.Fatal("expected notification_type column to be added")
	}
	if !db.Migrator().HasTable("schema_migrations") {
		t.Fatal("expected obsolete ledger to remain untouched")
	}
}

func TestMigrateRejectsEditedAppliedMigration(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate sqlite schema: %v", err)
	}
	if err := db.Model(&schemaMigration{}).Where("version = ?", 1).Update("checksum", "edited").Error; err != nil {
		t.Fatalf("tamper migration checksum: %v", err)
	}

	err := repositories.Migrate()
	if !errors.Is(err, ErrMigrationChecksumMismatch) {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestMigrationFailureCanRerunAndDoesNotRecordSuccessEarly(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	failed := []migration{{
		Version: 1, Name: "recoverable", Definition: "create recoverable_table",
		Source: "up: injected failure; schema: recoverable_table(id integer primary key)",
		Up:     func(tx *gorm.DB) error { return errors.New("injected failure") },
	}}
	if err := runMigrations(db, failed); err == nil {
		t.Fatal("expected injected migration failure")
	}
	var ledgerCount int64
	if err := db.Model(&schemaMigration{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count ledger after failure: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected no ledger row after failure, got %d", ledgerCount)
	}

	recovered := []migration{{
		Version: 1, Name: "recoverable", Definition: "create recoverable_table",
		Source: "up: injected failure; schema: recoverable_table(id integer primary key)",
		Up: func(tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE IF NOT EXISTS recoverable_table (id integer primary key)").Error
		},
	}}
	if err := runMigrations(db, recovered); err != nil {
		t.Fatalf("rerun recovered migration: %v", err)
	}
	if !db.Migrator().HasTable("recoverable_table") {
		t.Fatal("expected recovered migration table")
	}
}

func TestRollbackLastMigrationRunsReviewedInverse(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	migrations := []migration{{
		Version: 1, Name: "reversible", Definition: "create reversible_table; down drops only that empty table",
		Source: "up: create reversible_table; down: drop reversible_table if exists",
		Up: func(tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE reversible_table (id integer primary key)").Error
		},
		Down: func(tx *gorm.DB) error { return tx.Exec("DROP TABLE IF EXISTS reversible_table").Error },
	}}
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("apply reversible migration: %v", err)
	}
	if err := rollbackLastMigration(db, migrations); err != nil {
		t.Fatalf("roll back reversible migration: %v", err)
	}
	if db.Migrator().HasTable("reversible_table") {
		t.Fatal("expected reversible table to be removed")
	}
	var ledgerCount int64
	if err := db.Model(&schemaMigration{}).Count(&ledgerCount).Error; err != nil {
		t.Fatalf("count ledger after rollback: %v", err)
	}
	if ledgerCount != 0 {
		t.Fatalf("expected rollback to remove ledger row, got %d", ledgerCount)
	}
}

func TestRollbackRefusesForwardOnlyModuleMigrationWithoutChangingHistory(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	repositories := New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate baseline: %v", err)
	}
	want := insertRepresentativeHistory(t, db)
	if err := db.Model(&migration0003Case{}).Where("id = ?", want.CaseID).
		Updates(map[string]any{"status": "valid", "source": "discord"}).Error; err != nil {
		t.Fatalf("make post-migration history canonical: %v", err)
	}

	err := repositories.RollbackLastMigration()
	if !errors.Is(err, ErrMigrationNotReversible) {
		t.Fatalf("expected module migration forward-only refusal, got %v", err)
	}
	assertRepresentativeHistory(t, db, want)
}

func TestMigration0004SeedsSettingsRerunsAndRollsBackWithoutHistoryLoss(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	through0003 := registeredMigrations()[:3]
	if err := runMigrations(db, through0003); err != nil {
		t.Fatalf("apply migrations through 0003: %v", err)
	}
	want := insertRepresentativeHistory(t, db)
	through0004 := registeredMigrations()[:4]
	if err := runMigrations(db, through0004); err != nil {
		t.Fatalf("apply migration 0004: %v", err)
	}
	if err := runMigrations(db, through0004); err != nil {
		t.Fatalf("rerun migration 0004: %v", err)
	}
	var settings []migration0004GuildSettingsRecord
	if err := db.Find(&settings).Error; err != nil {
		t.Fatalf("list seeded guild settings: %v", err)
	}
	if len(settings) != 1 || settings[0].GuildID != want.GuildID || !settings[0].StarterPolicyNoticePending || settings[0].StarterPolicyTemplateID != "" {
		t.Fatalf("unexpected migration 0004 seed: %+v", settings)
	}
	assertRepresentativeHistory(t, db, want)
	if err := rollbackLastMigration(db, through0004); err != nil {
		t.Fatalf("roll back migration 0004: %v", err)
	}
	if db.Migrator().HasTable(&migration0004GuildSettingsRecord{}) {
		t.Fatal("guild settings table remained after rollback")
	}
	assertRepresentativeHistory(t, db, want)
	if err := runMigrations(db, through0004); err != nil {
		t.Fatalf("reapply migration 0004: %v", err)
	}
	var count int64
	if err := db.Model(&migration0004GuildSettingsRecord{}).Where("guild_id = ?", want.GuildID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("expected one re-seeded settings row, count=%d err=%v", count, err)
	}
}

func TestMigrationChecksumRejectsExecutableOrSchemaMutationWithoutIdentityChange(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	original := migration0001InitialV5Schema()
	if err := runMigrations(db, []migration{original}); err != nil {
		t.Fatalf("apply source-bound migration: %v", err)
	}

	schemaMutation := strings.Replace(original.Source, "NotifyUser       bool", "NotifyUser       string", 1)
	if schemaMutation == original.Source {
		t.Fatal("schema mutation fixture did not change embedded migration source")
	}
	for label, source := range map[string]string{
		"schema":     schemaMutation,
		"executable": original.Source + "\n// changed executable behavior",
	} {
		t.Run(label, func(t *testing.T) {
			ran := false
			mutated := original
			mutated.Source = source
			mutated.Up = func(*gorm.DB) error {
				ran = true
				return nil
			}
			err := runMigrations(db, []migration{mutated})
			if !errors.Is(err, ErrMigrationChecksumMismatch) {
				t.Fatalf("expected embedded-source checksum mismatch, got %v", err)
			}
			if ran {
				t.Fatal("mutated executable ran before checksum rejection")
			}
		})
	}
}

func TestRollbackDirtyStateBlocksStartupAndRecoversIdempotently(t *testing.T) {
	db := openSQLiteMigrationDB(t)
	failDownOnce := true
	migrations := []migration{{
		Version: 1, Name: "dirty_recovery", Definition: "create and reversibly drop dirty_recovery",
		Source: "up: create dirty_recovery; down: drop dirty_recovery if exists",
		Up: func(tx *gorm.DB) error {
			return tx.Exec("CREATE TABLE dirty_recovery (id integer primary key)").Error
		},
		Down: func(tx *gorm.DB) error {
			if err := tx.Exec("DROP TABLE IF EXISTS dirty_recovery").Error; err != nil {
				return err
			}
			if failDownOnce {
				failDownOnce = false
				return errors.New("injected failure after DDL")
			}
			return nil
		},
	}}
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("apply dirty-state fixture: %v", err)
	}
	if err := rollbackLastMigration(db, migrations); err == nil {
		t.Fatal("expected injected partial rollback failure")
	}
	var entry schemaMigration
	if err := db.First(&entry, "version = ?", 1).Error; err != nil {
		t.Fatalf("load dirty migration ledger row: %v", err)
	}
	if entry.State != migrationStateRollingBack || entry.RollbackStartedAt == nil {
		t.Fatalf("expected durable rolling_back state, got state=%q started=%v", entry.State, entry.RollbackStartedAt)
	}
	if err := runMigrations(db, migrations); !errors.Is(err, ErrMigrationDirty) {
		t.Fatalf("expected normal startup to refuse dirty migration, got %v", err)
	}

	if err := rollbackLastMigration(db, migrations); err != nil {
		t.Fatalf("recover dirty rollback idempotently: %v", err)
	}
	if err := runMigrations(db, migrations); err != nil {
		t.Fatalf("reapply migration after completed rollback: %v", err)
	}
}

// representativeHistory identifies immutable moderation records expected to survive migration operations.
type representativeHistory struct {
	GuildID, CaseID, AttemptID, EventID, AuditID string
	CaseNumber                                   uint64
	TemplateSnapshot                             string
}

// migration0003HistoryCase is the frozen pre-0003 case shape used to prove rejected columns survive migration.
type migration0003HistoryCase struct {
	ID                     string `gorm:"type:char(26);primaryKey"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
	GuildID                string
	CaseNumber             uint64
	TemplateVersion        uint
	TemplateSnapshotJSON   string `gorm:"column:template_snapshot_json"`
	TargetDiscordUserID    string
	ModeratorDiscordUserID string
	Reason                 string
	Severity               string
	Weight                 int
	Status                 string
	Source                 string
	MetadataJSON           string `gorm:"column:metadata_json"`
}

// TableName keeps the test fixture on the frozen cases table.
func (migration0003HistoryCase) TableName() string { return "cases" }

// migration0003LegacyEvent is the frozen event shape used to compare every preserved retired field.
type migration0003LegacyEvent struct {
	ID                 string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	CaseID             string
	GuildID            string
	EventType          string
	ActorDiscordUserID string
	ActorType          string
	Visibility         string
	Body               string
	MetadataJSON       string `gorm:"column:metadata_json"`
	EditedAt           *time.Time
	DeletedAt          *time.Time
}

// TableName keeps the test fixture on the frozen case_events table.
func (migration0003LegacyEvent) TableName() string { return "case_events" }

// assertMigration0003LegacyEventEqual verifies byte-bearing and lifecycle fields remain exact.
func assertMigration0003LegacyEventEqual(t *testing.T, got, want migration0003LegacyEvent) {
	t.Helper()
	if got.ID != want.ID || !got.CreatedAt.Equal(want.CreatedAt) || !got.UpdatedAt.Equal(want.UpdatedAt) ||
		got.CaseID != want.CaseID || got.GuildID != want.GuildID || got.EventType != want.EventType ||
		got.ActorDiscordUserID != want.ActorDiscordUserID || got.ActorType != want.ActorType || got.Visibility != want.Visibility ||
		got.Body != want.Body || got.MetadataJSON != want.MetadataJSON || !timePointersEqual(got.EditedAt, want.EditedAt) || !timePointersEqual(got.DeletedAt, want.DeletedAt) {
		t.Fatalf("legacy event changed:\n got=%+v\nwant=%+v", got, want)
	}
}

// timePointersEqual compares nullable timestamps without changing their precision.
func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

// insertRepresentativeHistory inserts IDs, numbering, snapshots, attempts, events, and audit rows from the current v5 shape.
func insertRepresentativeHistory(t *testing.T, db *gorm.DB) representativeHistory {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	want := representativeHistory{
		GuildID: "01J00000000000000000000001", CaseID: "01J00000000000000000000002",
		AttemptID: "01J00000000000000000000004", EventID: "01J00000000000000000000005",
		AuditID: "01J00000000000000000000006", CaseNumber: 42, TemplateSnapshot: `{"template":"preserved"}`,
	}
	records := []any{
		&GuildRecord{ULIDModelRecord: ULIDModelRecord{ID: want.GuildID, CreatedAt: now, UpdatedAt: now}, DiscordGuildID: "123", Name: "Fixture Guild", OwnerDiscordUserID: "owner"},
		&migration0003HistoryCase{ID: want.CaseID, CreatedAt: now, UpdatedAt: now, GuildID: want.GuildID, CaseNumber: want.CaseNumber, TemplateVersion: 3, TemplateSnapshotJSON: want.TemplateSnapshot, TargetDiscordUserID: "target", ModeratorDiscordUserID: "moderator", Reason: "preserve", Severity: "medium", Weight: 1, Status: "open", Source: "discord_command", MetadataJSON: "{}"},
		&CaseActionExecutionRecord{ULIDModelRecord: ULIDModelRecord{ID: "01J00000000000000000000003", CreatedAt: now, UpdatedAt: now}, CaseID: want.CaseID, Position: 1, ActionType: quack.ActionType("send_dm"), Status: quack.ActionExecutionStatus("succeeded"), IdempotencyKey: "fixture-action", ConfigSnapshotJSON: "{}"},
		&CaseActionAttemptRecord{ULIDModelRecord: ULIDModelRecord{ID: want.AttemptID, CreatedAt: now, UpdatedAt: now}, ExecutionID: "01J00000000000000000000003", AttemptNumber: 1, Status: quack.ActionAttemptStatus("succeeded"), StartedAt: now, RequestPayloadJSON: "{}", ResponsePayloadJSON: "{}"},
		&CaseEventRecord{ULIDModelRecord: ULIDModelRecord{ID: want.EventID, CreatedAt: now, UpdatedAt: now}, CaseID: want.CaseID, GuildID: want.GuildID, EventType: quack.CaseEventType("created"), ActorType: "staff", Visibility: quack.EventVisibility("staff"), Body: "fixture event", MetadataJSON: "{}"},
		&AuditLogEntryRecord{ULIDModelRecord: ULIDModelRecord{ID: want.AuditID, CreatedAt: now, UpdatedAt: now}, GuildID: want.GuildID, Source: quack.AuditSource("dashboard"), Action: "case.create", ResourceType: "case", ResourceID: want.CaseID, Result: quack.AuditResult("success"), MetadataJSON: "{}"},
	}
	for _, record := range records {
		query := db
		if _, ok := record.(*CaseActionExecutionRecord); ok && !db.Migrator().HasColumn(&CaseActionExecutionRecord{}, "LeaseToken") {
			query = query.Omit("LeaseToken", "LeaseExpiresAt", "DismissedAt", "DismissedByDiscordUserID", "ReversalOfExecutionID", "ReversalAppealID")
		}
		if err := query.Create(record).Error; err != nil {
			t.Fatalf("insert representative history %T: %v", record, err)
		}
	}
	var persisted CaseRecord
	if err := db.First(&persisted, "id = ?", want.CaseID).Error; err != nil {
		t.Fatalf("capture persisted representative case: %v", err)
	}
	want.TemplateSnapshot = persisted.TemplateSnapshotJSON
	return want
}

// assertRepresentativeHistory verifies the immutable identifiers and snapshots required by v5 history rules.
func assertRepresentativeHistory(t *testing.T, db *gorm.DB, want representativeHistory) {
	t.Helper()
	var guild GuildRecord
	if err := db.First(&guild, "id = ?", want.GuildID).Error; err != nil {
		t.Fatalf("load preserved guild: %v", err)
	}
	var caseRecord CaseRecord
	if err := db.First(&caseRecord, "id = ?", want.CaseID).Error; err != nil {
		t.Fatalf("load preserved case: %v", err)
	}
	if caseRecord.CaseNumber != want.CaseNumber || caseRecord.TemplateSnapshotJSON != want.TemplateSnapshot {
		t.Fatalf("case history changed: number=%d snapshot=%s", caseRecord.CaseNumber, caseRecord.TemplateSnapshotJSON)
	}
	for label, query := range map[string]any{
		"attempt": &CaseActionAttemptRecord{ULIDModelRecord: ULIDModelRecord{ID: want.AttemptID}},
		"event":   &CaseEventRecord{ULIDModelRecord: ULIDModelRecord{ID: want.EventID}},
		"audit":   &AuditLogEntryRecord{ULIDModelRecord: ULIDModelRecord{ID: want.AuditID}},
	} {
		if err := db.First(query).Error; err != nil {
			t.Fatalf("load preserved %s: %v", label, err)
		}
	}
}
