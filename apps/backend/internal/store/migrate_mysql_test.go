package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestMySQL runs the dialect-specific parts of the store against a real
// MySQL: the baseline and its generated-column constraint, the migration
// lock, rollback, and the raw SQL in polling and statistics. It needs
// QUACK_TEST_MYSQL_DSN and creates and drops its own database.
func TestMySQL(t *testing.T) {
	db := openMySQL(t)
	s := store.New(db, nil)
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Go(func() { errs[i] = s.Migrate() })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("concurrent migrate: %v", err)
	}
	assertSchema(t, db, true)
	if version, err := s.MigrationReadiness(context.Background()); err != nil || version != 1 {
		t.Fatalf("MigrationReadiness = %d, %v", version, err)
	}

	t.Run("one default level", func(t *testing.T) {
		guildID := addGuild(t, s, "mysql-levels")
		template := createTemplate(t, s, guildID, "spam")
		err := db.Table("case_template_levels").Where("id = ?", template.Levels[1].Level.ID).Update("is_default", true).Error
		if err == nil {
			t.Fatal("second default level accepted")
		}
	})

	t.Run("workflow", func(t *testing.T) {
		ctx := context.Background()
		guildID := addGuild(t, s, "mysql-workflow")
		notifyOnly := createCase(t, s, guildID, nil, pendingNotification())
		enforced := createCase(t, s, guildID, timeout(0), pendingNotification())
		if ids := poll(t, s, 10); !slices.Contains(ids, notifyOnly.Case.ID) || !slices.Contains(ids, enforced.Case.ID) {
			t.Fatalf("poll = %v, want both cases", ids)
		}
		complete(t, s, claim(t, s, enforced.Case.ID), quack.ActionExecutionSucceeded)
		summary, err := s.TargetCaseSummary(ctx, guildID, "target-1")
		if err != nil || summary.Total != 2 {
			t.Fatalf("summary = %+v, %v", summary, err)
		}
		now := time.Now().UTC()
		stats, err := s.DeriveStaffStatistics(ctx, quack.StaffStatisticsParams{
			GuildID: guildID,
			From:    now.Add(-time.Hour),
			To:      now.Add(time.Hour),
		})
		if err != nil || stats.CaseTotal != 2 || len(stats.CasesByDay) == 0 || stats.CasesByDay[0].Key != now.Format(time.DateOnly) {
			t.Fatalf("stats = %+v, %v", stats, err)
		}
		if _, err := s.ActionQueueSnapshot(ctx, guildID, 10); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
	})

	if err := s.Rollback(false); !errors.Is(err, store.ErrBaselineRollback) {
		t.Fatalf("Rollback(false) = %v, want ErrBaselineRollback", err)
	}
	if err := s.Rollback(true); err != nil {
		t.Fatalf("Rollback(true): %v", err)
	}
	assertSchema(t, db, false)
	if err := s.Migrate(); err != nil {
		t.Fatalf("migrate after rollback: %v", err)
	}
	assertSchema(t, db, true)
}

// openMySQL creates a throwaway database on QUACK_TEST_MYSQL_DSN and drops it
// when the test ends.
func openMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not set")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse QUACK_TEST_MYSQL_DSN: %v", err)
	}
	quiet := &gorm.Config{Logger: logger.Discard}
	adminCfg := *cfg
	adminCfg.DBName = ""
	admin, err := gorm.Open(gormmysql.Open(adminCfg.FormatDSN()), quiet)
	if err != nil {
		t.Fatalf("connect to MySQL: %v", err)
	}
	name := fmt.Sprintf("quack_store_%d", time.Now().UnixNano())
	if err := admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
		t.Fatalf("create test database: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP DATABASE IF EXISTS `" + name + "`").Error; err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	testCfg := *cfg
	testCfg.DBName = name
	testCfg.ParseTime = true
	db, err := gorm.Open(gormmysql.Open(testCfg.FormatDSN()), quiet)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
