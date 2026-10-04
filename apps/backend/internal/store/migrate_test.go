package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/gorm"
)

// tables is every table the baseline owns, including the module tables.
var tables = []string{
	"guilds", "guild_settings", "staff_members",
	"case_templates", "case_template_context_fields", "case_template_levels", "case_template_level_actions",
	"cases", "case_action_executions", "case_action_attempts", "case_evidence_snapshots", "case_evidence_attachments",
	"case_notifications", "case_events",
	"appeals", "appeal_events", "appeal_notifications",
	"audit_log_entries", "audit_mirror_deliveries", "v4_import_batches", "v4_import_sources",
	"module_configurations", "tickets", "ticket_events", "ticket_transcripts", "ticket_member_states", "ticket_message_journal",
	"honeypot_triggers", "honeypot_message_cleanups", "honeypot_warning_refreshes",
}

func TestMigrateCreatesSchemaOnce(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	s := store.New(db, nil)
	if err := s.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	assertSchema(t, db, true)
	var ledger []struct {
		Version uint64
		Name    string
	}
	if err := db.Raw("SELECT version, name FROM quack_schema_migrations").Scan(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if len(ledger) != 3 || ledger[0].Name != "baseline" || ledger[1].Version != 2 || ledger[1].Name != "audit_mirror_deliveries" ||
		ledger[2].Version != 3 || ledger[2].Name != "launch_announcement" {
		t.Fatalf("ledger = %+v, want the baseline, audit_mirror_deliveries, and launch_announcement", ledger)
	}
	if version, err := s.MigrationReadiness(context.Background()); err != nil || version != 3 {
		t.Fatalf("MigrationReadiness = %d, %v; want 3, nil", version, err)
	}
	for _, dropped := range []string{"action_manual_reviews", "module_import_records",
		"quack_v5_0002_template_compatibility", "quack_v5_0003_case_compatibility"} {
		if db.Migrator().HasTable(dropped) {
			t.Errorf("retired table %s exists", dropped)
		}
	}
	for table, column := range map[string]string{"cases": "validity", "case_action_executions": "status"} {
		if !db.Migrator().HasColumn(table, column) {
			t.Errorf("%s has no %s column", table, column)
		}
	}
	for table, column := range map[string]string{"cases": "status", "case_action_executions": "irreversible", "case_templates": "deleted_at"} {
		if db.Migrator().HasColumn(table, column) {
			t.Errorf("%s still has retired column %s", table, column)
		}
	}
}

// TestMigrateCatchesUpOlderBaseline covers a database that recorded the
// baseline before the record structs gained or lost tables and columns.
func TestMigrateCatchesUpOlderBaseline(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	if err := db.Migrator().DropTable("case_publications"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE guild_settings DROP COLUMN appeal_queue_channel_discord_id").Error; err != nil {
		t.Fatal(err)
	}
	// Custom appeal forms kept their questions in guild_appeal_settings and
	// each appeal's statement in answers_json, beside an unused content.
	for _, statement := range []string{
		"ALTER TABLE appeals RENAME COLUMN statement TO content",
		"CREATE TABLE guild_appeal_settings (id CHAR(26) PRIMARY KEY, questions_json TEXT NOT NULL)",
		"ALTER TABLE appeals ADD COLUMN question_snapshot_json TEXT NOT NULL DEFAULT '[]'",
		"ALTER TABLE appeals ADD COLUMN answers_json TEXT NOT NULL DEFAULT '[]'",
		`INSERT INTO appeals (id, created_at, updated_at, guild_id, target_discord_user_id, status, content, answers_json, metadata_json)
			VALUES ('appeal', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'guild', 'member', 'pending', '',
			'[{"question_id":"reason","value":" Please reconsider. "},{"question_id":"contact","value":true}]', '{}')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := store.New(db, nil).Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if db.Migrator().HasTable("guild_appeal_settings") {
		t.Error("guild_appeal_settings was not dropped")
	}
	for _, column := range []string{"question_snapshot_json", "answers_json"} {
		if db.Migrator().HasColumn("appeals", column) {
			t.Errorf("appeals.%s was not dropped", column)
		}
	}
	var statement string
	if err := db.Raw("SELECT statement FROM appeals WHERE id = 'appeal'").Scan(&statement).Error; err != nil || statement != "Please reconsider." {
		t.Errorf("appeal statement = %q, %v; want the reason answer", statement, err)
	}
	if !db.Migrator().HasTable("case_publications") {
		t.Error("case_publications was not created")
	}
	if !db.Migrator().HasColumn("guild_settings", "appeal_queue_channel_discord_id") {
		t.Error("guild_settings.appeal_queue_channel_discord_id was not added")
	}
}

// TestMigrateAuditMirrorDeliveries upgrades a baseline-only database whose
// audit log already holds important entries, mirrored or not. None of them
// may be queued, so nothing is posted twice; entries written afterwards are.
func TestMigrateAuditMirrorDeliveries(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewSQLiteDB(t)
	for _, statement := range []string{
		"DROP TABLE audit_mirror_deliveries",
		"DELETE FROM quack_schema_migrations WHERE version >= 2",
		`INSERT INTO guild_settings (id, created_at, updated_at, guild_id, audit_mirror_channel_discord_id,
			notification_introduction, notification_footer, starter_policy_notice_pending)
			VALUES ('settings', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'guild', '123', '', '', false)`,
		`INSERT INTO audit_log_entries (id, created_at, updated_at, guild_id, source, action, resource_type, resource_id, result, metadata_json)
			VALUES ('01AAAAAAAAAAAAAAAAAAAAAAAA', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'guild', 'api', 'case.create', 'case', 'c1', 'success', '{}'),
			('01AAAAAAAAAAAAAAAAAAAAAAAB', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'guild', 'system', 'audit_mirror.delivered', 'audit_entry', '01AAAAAAAAAAAAAAAAAAAAAAAA', 'success', '{}'),
			('01AAAAAAAAAAAAAAAAAAAAAAAC', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'guild', 'api', 'case.void', 'case', 'c1', 'success', '{}')`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatal(err)
		}
	}
	s := store.New(db, nil)
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if version, err := s.MigrationReadiness(ctx); err != nil || version != 3 {
		t.Fatalf("MigrationReadiness = %d, %v; want 3", version, err)
	}
	var queued int64
	if err := db.Table("audit_mirror_deliveries").Count(&queued).Error; err != nil || queued != 0 {
		t.Fatalf("%d deliveries queued for existing entries (err %v), want none", queued, err)
	}
	if claimed, err := s.ClaimAuditMirrorDeliveries(ctx, 10); err != nil || len(claimed) != 0 {
		t.Fatalf("claimed %+v, %v; want nothing from before the migration", claimed, err)
	}
	entry := quack.AuditLogEntry{GuildID: "guild", Source: quack.AuditSourceAPI, Action: string(quack.AuditActionCaseCreate),
		ResourceType: "case", ResourceID: "c2", Result: quack.AuditResultSuccess}
	if err := s.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimAuditMirrorDeliveries(ctx, 10); err != nil || len(claimed) != 1 || claimed[0].Entry.ID != entry.ID {
		t.Fatalf("claimed %+v, %v; want the new entry", claimed, err)
	}
}

func TestMigrateRefusesUnknownLedger(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	future := "INSERT INTO quack_schema_migrations (version, name, applied_at) " +
		"VALUES (4, 'from_the_future', CURRENT_TIMESTAMP)"
	if err := db.Exec(future).Error; err != nil {
		t.Fatal(err)
	}
	if err := store.New(db, nil).Migrate(); err == nil {
		t.Fatal("Migrate accepted a ledger newer than the binary")
	}
}

func TestRollbackBaseline(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	s := store.New(db, nil)
	if err := s.Rollback(false); err != nil {
		t.Fatalf("Rollback(false) of launch_announcement: %v", err)
	}
	if db.Migrator().HasColumn("guild_settings", "launch_announced_at") {
		t.Fatal("rolling back launch_announcement kept its column")
	}
	for _, dropAll := range []bool{false, true} {
		if err := s.Rollback(dropAll); !errors.Is(err, store.ErrIrreversible) {
			t.Fatalf("Rollback(%v) past audit_mirror_deliveries = %v, want ErrIrreversible", dropAll, err)
		}
	}
	assertSchema(t, db, true)
	// From here on, a database that only ever applied the baseline.
	if err := db.Exec("DELETE FROM quack_schema_migrations WHERE version = 2").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Rollback(false); !errors.Is(err, store.ErrBaselineRollback) {
		t.Fatalf("Rollback(false) = %v, want ErrBaselineRollback", err)
	}
	assertSchema(t, db, true)

	if err := s.Rollback(true); err != nil {
		t.Fatalf("Rollback(true): %v", err)
	}
	assertSchema(t, db, false)
	if _, err := s.MigrationReadiness(context.Background()); err == nil {
		t.Error("MigrationReadiness passed with no migrations applied")
	}
	if err := s.Rollback(true); err != nil {
		t.Fatalf("Rollback with nothing applied: %v", err)
	}

	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate after rollback: %v", err)
	}
	assertSchema(t, db, true)
}

// TestSchemaConstraints checks that every invariant the schema owns rejects
// a violating write.
func TestSchemaConstraints(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	db := s.DB()
	otherGuildID := addGuild(t, s, "guild-2")
	template := createTemplate(t, s, guildID, "spam")
	other := createTemplate(t, s, otherGuildID, "other")
	first := createCase(t, s, guildID, timeout(0), pendingNotification())
	second := createCase(t, s, guildID, timeout(0), pendingNotification())
	firstAppeal := createAppeal(t, s, guildID, first.Case.ID)
	secondAppeal := createAppeal(t, s, guildID, second.Case.ID)
	var appealNotifications []string
	if err := db.Table("appeal_notifications").Order("created_at").Pluck("id", &appealNotifications).Error; err != nil {
		t.Fatal(err)
	}
	var appealEvents []string
	if err := db.Table("appeal_events").Where("appeal_id = ?", firstAppeal.ID).Pluck("id", &appealEvents).Error; err != nil {
		t.Fatal(err)
	}

	collisions := []struct {
		name, table, column string
		value               any
		id                  string
	}{
		{"case number per guild", "cases", "case_number", first.Case.CaseNumber, second.Case.ID},
		{"execution idempotency key", "case_action_executions", "idempotency_key",
			first.ActionExecutions[0].IdempotencyKey, second.ActionExecutions[0].ID},
		{"one notification per case", "case_notifications", "case_id", first.Case.ID, second.Notification.ID},
		{"one appeal per case", "appeals", "case_id", first.Case.ID, secondAppeal.ID},
		{"one notification per appeal event", "appeal_notifications", "event_id", appealEvents[0], appealNotifications[1]},
		{"one action per level", "case_template_level_actions", "level_id",
			template.Levels[1].Level.ID, other.Levels[1].Actions[0].ID},
		{"one default level per template", "case_template_levels", "is_default", true, template.Levels[1].Level.ID},
	}
	for _, c := range collisions {
		t.Run(c.name, func(t *testing.T) {
			err := db.Table(c.table).Where("id = ?", c.id).Update(c.column, c.value).Error
			if err == nil {
				t.Fatalf("UPDATE %s SET %s succeeded; want a unique violation", c.table, c.column)
			}
		})
	}

	t.Run("template slug per guild", func(t *testing.T) {
		_, err := s.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{Template: newTemplate(guildID, "spam"), Levels: newLevels()})
		if err == nil {
			t.Fatal("duplicate slug in one guild succeeded")
		}
		params := quack.CreateCaseTemplateParams{Template: newTemplate(otherGuildID, "spam"), Levels: newLevels()}
		if _, err := s.CreateCaseTemplate(ctx, params); err != nil {
			t.Fatalf("same slug in another guild: %v", err)
		}
	})

	t.Run("case idempotency key per guild", func(t *testing.T) {
		key := "request-1"
		c := newCase(guildID, nil)
		c.IdempotencyKey = &key
		if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()}); err == nil {
			t.Fatal("duplicate idempotency key in one guild succeeded")
		}
		c.GuildID = otherGuildID
		if _, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: c, Event: newCaseEvent()}); err != nil {
			t.Fatalf("same idempotency key in another guild: %v", err)
		}
	})

	t.Run("honeypot message claimed once", func(t *testing.T) {
		triggers := honeypot.NewStore(db)
		message := honeypot.Message{GuildID: guildID, ChannelDiscordID: "trap", MessageDiscordID: "m1", AuthorDiscordUserID: "u1"}
		if _, created, err := triggers.Claim(ctx, message, template.Template.ID, honeypot.OutcomePending); err != nil || !created {
			t.Fatalf("first claim = %v, %v", created, err)
		}
		if _, created, err := triggers.Claim(ctx, message, template.Template.ID, honeypot.OutcomePending); err != nil || created {
			t.Fatalf("second claim = %v, %v; want a no-op", created, err)
		}
	})
}

// assertSchema checks that every baseline table exists, or that none does.
func assertSchema(t *testing.T, db *gorm.DB, exists bool) {
	t.Helper()
	for _, table := range tables {
		if db.Migrator().HasTable(table) != exists {
			t.Errorf("table %s exists = %v, want %v", table, !exists, exists)
		}
	}
}
