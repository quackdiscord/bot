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
	"appeals", "appeal_events", "guild_appeal_settings", "appeal_notifications",
	"audit_log_entries", "v4_import_batches", "v4_import_sources",
	"module_configurations", "tickets", "ticket_events", "ticket_transcripts", "ticket_member_states",
	"honeypot_triggers",
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
	if len(ledger) != 1 || ledger[0].Version != 1 || ledger[0].Name != "baseline" {
		t.Fatalf("ledger = %+v, want only the baseline", ledger)
	}
	if version, err := s.MigrationReadiness(context.Background()); err != nil || version != 1 {
		t.Fatalf("MigrationReadiness = %d, %v; want 1, nil", version, err)
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

func TestMigrateRefusesUnknownLedger(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	if err := db.Exec("INSERT INTO quack_schema_migrations (version, name, applied_at) VALUES (2, 'from_the_future', CURRENT_TIMESTAMP)").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.New(db, nil).Migrate(); err == nil {
		t.Fatal("Migrate accepted a ledger newer than the binary")
	}
}

func TestRollbackBaseline(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	s := store.New(db, nil)
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
		if _, err := s.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{Template: newTemplate(otherGuildID, "spam"), Levels: newLevels()}); err != nil {
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
