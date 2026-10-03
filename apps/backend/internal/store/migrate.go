package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
)

// migration is one schema step. Steps run in version order, once each, and
// are recorded in the ledger table. MySQL commits DDL immediately, so a step
// that fails halfway is rerun from the start: write steps so that is safe.
type migration struct {
	version uint64
	name    string
	up      func(*gorm.DB) error
	// down undoes up, or is nil when the step cannot be undone.
	down func(*gorm.DB) error
}

// migrations is the schema history. Append new steps with the next version;
// never edit one that has shipped.
var migrations = []migration{
	{version: 1, name: "baseline", up: createBaseline, down: dropBaseline},
}

// ErrIrreversible means the newest migration has no down step.
var ErrIrreversible = errors.New("migration cannot be rolled back")

// ErrBaselineRollback means a rollback would undo the baseline, dropping every
// table and all data, without the caller confirming that.
var ErrBaselineRollback = errors.New("rolling back the baseline drops every table")

// ledgerRecord is one applied migration. The ledger is not in tables(): it
// must exist before the baseline and outlive rolling it back.
type ledgerRecord struct {
	Version   uint64    `gorm:"primaryKey;autoIncrement:false"`
	Name      string    `gorm:"size:191;not null"`
	AppliedAt time.Time `gorm:"not null"`
}

func (ledgerRecord) TableName() string { return "quack_schema_migrations" }

// Migrate applies every pending migration. Concurrent callers on MySQL wait
// for each other, so every replica can migrate on startup.
func (s *Store) Migrate() error {
	return withMigrationLock(s.db, func(db *gorm.DB) error {
		if err := withTableOptions(db).AutoMigrate(&ledgerRecord{}); err != nil {
			return fmt.Errorf("create migration ledger: %w", err)
		}
		applied, err := appliedMigrations(db)
		if err != nil {
			return err
		}
		for _, m := range migrations[len(applied):] {
			if err := m.up(db); err != nil {
				return fmt.Errorf("apply migration %d %s: %w", m.version, m.name, err)
			}
			entry := ledgerRecord{Version: m.version, Name: m.name, AppliedAt: time.Now().UTC()}
			if err := db.Create(&entry).Error; err != nil {
				return fmt.Errorf("record migration %d %s: %w", m.version, m.name, err)
			}
		}
		if len(applied) == 0 {
			return nil
		}
		// The baseline is the record structs, which keep gaining tables and
		// columns. Re-running it adds whatever an older database is missing;
		// AutoMigrate never drops or renames anything, so those changes are
		// made by hand first.
		if err := retireAppealForms(db); err != nil {
			return fmt.Errorf("retire custom appeal forms: %w", err)
		}
		if err := createBaseline(db); err != nil {
			return fmt.Errorf("bring baseline up to date: %w", err)
		}
		return nil
	})
}

// Rollback undoes the newest applied migration. Undoing the baseline drops
// every table, so it returns ErrBaselineRollback unless dropAll is set.
func (s *Store) Rollback(dropAll bool) error {
	return withMigrationLock(s.db, func(db *gorm.DB) error {
		applied, err := appliedMigrations(db)
		if err != nil {
			return err
		}
		if len(applied) == 0 {
			return nil
		}
		m := migrations[len(applied)-1]
		switch {
		case m.down == nil:
			return fmt.Errorf("%w: %d %s", ErrIrreversible, m.version, m.name)
		case m.version == 1 && !dropAll:
			return ErrBaselineRollback
		}
		if err := m.down(db); err != nil {
			return fmt.Errorf("roll back migration %d %s: %w", m.version, m.name, err)
		}
		if err := db.Delete(&ledgerRecord{}, "version = ?", m.version).Error; err != nil {
			return fmt.Errorf("unrecord migration %d %s: %w", m.version, m.name, err)
		}
		return nil
	})
}

// MigrationReadiness returns the applied schema version, or an error when the
// database is behind this binary or has migrations it does not know.
func (s *Store) MigrationReadiness(ctx context.Context) (uint64, error) {
	applied, err := appliedMigrations(s.db.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	if len(applied) < len(migrations) {
		return 0, fmt.Errorf("schema is behind: applied %d of %d migrations", len(applied), len(migrations))
	}
	return applied[len(applied)-1].Version, nil
}

// appliedMigrations reads the ledger and checks that it is a prefix of
// migrations. A database with no ledger has nothing applied.
func appliedMigrations(db *gorm.DB) ([]ledgerRecord, error) {
	if !db.Migrator().HasTable(&ledgerRecord{}) {
		return nil, nil
	}
	var applied []ledgerRecord
	if err := db.Order("version ASC").Find(&applied).Error; err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	if len(applied) > len(migrations) {
		return nil, fmt.Errorf("schema has %d migrations but this binary knows %d", len(applied), len(migrations))
	}
	for i, entry := range applied {
		if m := migrations[i]; entry.Version != m.version || entry.Name != m.name {
			return nil, fmt.Errorf("migration ledger entry %d %q does not match %d %q",
				entry.Version, entry.Name, m.version, m.name)
		}
	}
	return applied, nil
}

// createBaseline creates every table, then the constraints struct tags cannot
// express.
func createBaseline(db *gorm.DB) error {
	if err := withTableOptions(db).AutoMigrate(tables()...); err != nil {
		return err
	}
	return oneDefaultLevel(db)
}

// oneDefaultLevel enforces at most one default level per template. MySQL has
// no partial indexes, so it indexes a generated column that holds the
// template ID only on the default level; NULLs never collide.
func oneDefaultLevel(db *gorm.DB) error {
	const index = "idx_levels_one_default"
	if db.Migrator().HasIndex(&levelRecord{}, index) {
		return nil
	}
	if db.Dialector.Name() != "mysql" {
		return db.Exec("CREATE UNIQUE INDEX " + index + " ON case_template_levels (template_id) WHERE is_default").Error
	}
	if !db.Migrator().HasColumn(&levelRecord{}, "default_template_id") {
		if err := db.Exec(`ALTER TABLE case_template_levels ADD COLUMN default_template_id CHAR(26)
			GENERATED ALWAYS AS (CASE WHEN is_default THEN template_id END) STORED`).Error; err != nil {
			return err
		}
	}
	return db.Exec("CREATE UNIQUE INDEX " + index + " ON case_template_levels (default_template_id)").Error
}

// retireAppealForms removes what custom appeal forms left in a database
// migrated before every appeal used one fixed question: the
// guild_appeal_settings table and the appeals form columns, whose NOT NULL
// constraints would otherwise reject new appeals. The unused content column
// becomes statement, and statements saved only as form answers are copied
// into it. Every step is safe to rerun.
func retireAppealForms(db *gorm.DB) error {
	m := db.Migrator()
	if m.HasColumn("appeals", "content") && !m.HasColumn("appeals", "statement") {
		if err := db.Exec("ALTER TABLE appeals RENAME COLUMN content TO statement").Error; err != nil {
			return err
		}
	}
	if m.HasColumn("appeals", "answers_json") {
		var rows []struct{ ID, AnswersJSON string }
		if err := db.Table("appeals").Select("id, answers_json").Where("statement = ''").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			var answers []struct {
				Value any `json:"value"`
			}
			if json.Unmarshal([]byte(row.AnswersJSON), &answers) != nil {
				continue
			}
			var parts []string
			for _, answer := range answers {
				if text, ok := answer.Value.(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
			statement := strings.Join(parts, "\n\n")
			if err := db.Table("appeals").Where("id = ?", row.ID).Update("statement", statement).Error; err != nil {
				return err
			}
		}
	}
	for _, column := range []string{"question_snapshot_json", "answers_json"} {
		if m.HasColumn("appeals", column) {
			// Plain SQL: the SQLite driver's DropColumn cannot drop a
			// column the record struct no longer has.
			if err := db.Exec("ALTER TABLE appeals DROP COLUMN " + column).Error; err != nil {
				return err
			}
		}
	}
	return m.DropTable("guild_appeal_settings")
}

// dropBaseline drops every table, children first.
func dropBaseline(db *gorm.DB) error {
	for _, table := range slices.Backward(tables()) {
		if err := db.Migrator().DropTable(table); err != nil {
			return err
		}
	}
	return nil
}

// withTableOptions makes new MySQL tables InnoDB and utf8mb4.
func withTableOptions(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() != "mysql" {
		return db
	}
	return db.Set("gorm:table_options", "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci")
}

// withMigrationLock runs fn while holding a MySQL named lock, so concurrent
// migrators take turns. SQLite, used only in tests, needs no lock.
func withMigrationLock(db *gorm.DB, fn func(*gorm.DB) error) error {
	if db.Dialector.Name() != "mysql" {
		return fn(db)
	}
	const name = "quack_schema_migrations"
	ctx := context.Background()
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get database handle for migration lock: %w", err)
	}
	// Named locks belong to a connection, so hold one connection for the
	// lock's lifetime.
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve connection for migration lock: %w", err)
	}
	defer conn.Close()
	var acquired int
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, 30)", name).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	if acquired != 1 {
		return errors.New("acquire migration lock: timed out")
	}
	defer func() {
		var released any
		_ = conn.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", name).Scan(&released)
	}()
	return fn(db)
}
