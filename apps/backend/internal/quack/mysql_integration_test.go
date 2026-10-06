package quack_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	mysqlconfig "github.com/go-sql-driver/mysql"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestMySQLConcurrentCaseCreationSelectsDistinctEscalation(t *testing.T) {
	db := openIsolatedMySQLDB(t)
	repositories := store.New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UTC().UnixNano())
	guild, err := repositories.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID: "integration-" + suffix, Name: "Integration", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	staff := &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}
	guildContext := &quack.GuildStaffContext{
		Guild: guild, Staff: staff, IsAdmin: true, IsModerator: true,
		Permissions: map[quack.PermissionAction]bool{
			quack.PermissionActionCaseTemplateWrite: true,
			quack.PermissionActionCaseCreate:        true,
		},
	}
	services := quack.New(quack.Deps{Store: repositories})
	template, err := services.Templates.Create(ctx, guildContext, quack.TemplateInput{
		Slug: "concurrent-" + suffix, Name: "Concurrent", Description: "Integration", ReasonTemplate: "Reason",
		Levels: []quack.TemplateLevelInput{
			{Name: "Default", Position: 1, IsDefault: true},
			{Name: "Second", Position: 2, TriggerCaseCount: 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	results := make(chan *quack.CaseResponse, 2)
	errors := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(2)
	for range 2 {
		go func() {
			start.Done()
			start.Wait()
			created, createErr := services.Cases.Create(ctx, guildContext, quack.CaseInput{
				TemplateID: template.ID, TargetDiscordUserID: "target",
			})
			if createErr != nil {
				errors <- createErr
				return
			}
			results <- created
		}()
	}

	created := make([]*quack.CaseResponse, 0, 2)
	for range 2 {
		select {
		case err := <-errors:
			t.Fatal(err)
		case result := <-results:
			created = append(created, result)
		case <-time.After(10 * time.Second):
			t.Fatal("timed out waiting for concurrent case creation")
		}
	}
	if created[0].CaseNumber == created[1].CaseNumber {
		t.Fatalf("duplicate case number %d", created[0].CaseNumber)
	}
	levels := map[string]bool{}
	for _, result := range created {
		levels[result.SelectedLevel.Name] = true
	}
	if !levels["Default"] || !levels["Second"] {
		t.Fatalf("expected default and second escalation levels, got %+v", levels)
	}
}

func TestMySQLConcurrentCaseCreationAndVoidPreserveNumberingAndValidity(t *testing.T) {
	db := openIsolatedMySQLDB(t)
	repositories := store.New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	suffix := fmt.Sprint(time.Now().UTC().UnixNano())
	guild, err := repositories.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "create-void-" + suffix, Name: "Create Void", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	guildContext := &quack.GuildStaffContext{
		Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, IsAdmin: true, IsModerator: true,
		Permissions: map[quack.PermissionAction]bool{quack.PermissionActionCaseTemplateWrite: true, quack.PermissionActionCaseCreate: true, quack.PermissionActionCaseVoid: true},
	}
	services := quack.New(quack.Deps{Store: repositories})
	template, err := services.Templates.Create(ctx, guildContext, quack.TemplateInput{
		Slug: "create-void-" + suffix, Name: "Create Void", ReasonTemplate: "Reason",
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}, {Name: "Second", Position: 2, TriggerCaseCount: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := services.Cases.Create(ctx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target"})
	if err != nil {
		t.Fatal(err)
	}

	errors := make(chan error, 2)
	created := make(chan *quack.CaseResponse, 1)
	start := make(chan struct{})
	go func() {
		<-start
		item, createErr := services.Cases.Create(ctx, guildContext, quack.CaseInput{TemplateID: template.ID, TargetDiscordUserID: "target"})
		if createErr == nil {
			created <- item
		}
		errors <- createErr
	}()
	go func() {
		<-start
		_, voidErr := services.Cases.Void(ctx, guildContext, first.ID, "concurrent correction")
		errors <- voidErr
	}()
	close(start)
	for range 2 {
		if operationErr := <-errors; operationErr != nil {
			t.Fatalf("concurrent create/void: %v", operationErr)
		}
	}
	second := <-created
	if second.CaseNumber != first.CaseNumber+1 {
		t.Fatalf("case numbering changed across create/void: first=%d second=%d", first.CaseNumber, second.CaseNumber)
	}
	persistedFirst, err := repositories.GetCaseByID(ctx, first.ID)
	if err != nil || persistedFirst.Validity != quack.CaseValidityVoided {
		t.Fatalf("first case was not durably voided: %+v err=%v", persistedFirst, err)
	}
	persistedSecond, err := repositories.GetCaseByID(ctx, second.ID)
	if err != nil || persistedSecond.Validity != quack.CaseValidityValid {
		t.Fatalf("concurrent case was not durably valid: %+v err=%v", persistedSecond, err)
	}
}

func TestMySQLUnavailableEvidenceSnapshotUsesPersistableTimestamp(t *testing.T) {
	db := openIsolatedMySQLDB(t)
	repositories := store.New(db, nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	guild, err := repositories.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID: "111111111111111111", Name: "Evidence", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	guildContext := &quack.GuildStaffContext{
		Guild: guild, Staff: &quack.StaffMember{GuildID: guild.ID, DiscordUserID: "moderator"}, ActorDiscordUserID: "moderator",
		Permissions: map[quack.PermissionAction]bool{
			quack.PermissionActionCaseTemplateWrite: true,
			quack.PermissionActionCaseCreate:        true,
		},
	}
	template, err := quack.NewTemplateService(repositories).Create(ctx, guildContext, quack.TemplateInput{
		Slug: "unavailable-evidence", Name: "Unavailable evidence", Description: "Integration", ReasonTemplate: "Reason",
		ContextFields: []quack.TemplateContextFieldInput{
			{Key: "summary", Label: "Summary", FieldType: quack.ContextFieldShortText, Position: 1, Required: true},
			{Key: "message", Label: "Message", FieldType: quack.ContextFieldMessageLink, Position: 2, Required: true},
		},
		Levels: []quack.TemplateLevelInput{{Name: "Default", Position: 1, IsDefault: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	link := "https://discord.com/channels/111111111111111111/222222222222222222/333333333333333333"
	summary, _ := json.Marshal("visible fallback")
	message, _ := json.Marshal(link)
	service := quack.NewCaseService(repositories, nil, quack.NewEvidenceService(unavailableEvidenceClient{err: &quack.EvidenceUnavailableError{Outcome: "deleted", Message: "message deleted"}}), nil)
	created, err := service.Create(ctx, guildContext, quack.CaseInput{
		TemplateID: template.ID, TargetDiscordUserID: "target",
		ContextValues: []quack.CaseContextValueInput{{Key: "summary", Value: summary}, {Key: "message", Value: message}},
	})
	if err != nil {
		t.Fatalf("create partial-evidence case on MySQL: %v", err)
	}
	evidence, _, err := repositories.ListCaseEvidence(ctx, created.ID)
	if err != nil || len(evidence) != 1 || evidence[0].MessageCreatedAt.IsZero() {
		t.Fatalf("persisted unavailable evidence is invalid: evidence=%+v err=%v", evidence, err)
	}
}

// TestMySQLGuildSettingsUpdateKeepsConcurrentStaffRoleChanges checks the
// staff role compare-and-swap under MySQL's row locks.
func TestMySQLGuildSettingsUpdateKeepsConcurrentStaffRoleChanges(t *testing.T) {
	repositories := store.New(openIsolatedMySQLDB(t), nil)
	if err := repositories.Migrate(); err != nil {
		t.Fatal(err)
	}
	testConcurrentStaffRoleChanges(t, repositories)
}

// openIsolatedMySQLDB creates a disposable database for a MySQL integration test.
func openIsolatedMySQLDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("QUACK_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("QUACK_TEST_MYSQL_DSN is not configured")
	}
	cfg, err := mysqlconfig.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse QUACK_TEST_MYSQL_DSN: %v", err)
	}
	cfg.ParseTime = true
	databaseName := fmt.Sprintf("quack_case_concurrency_%d", time.Now().UTC().UnixNano())
	adminCfg := *cfg
	adminCfg.DBName = ""
	adminDB, err := gorm.Open(gormmysql.Open(adminCfg.FormatDSN()), &gorm.Config{})
	if err != nil {
		t.Fatalf("open MySQL admin connection: %v", err)
	}
	if err := adminDB.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci").Error; err != nil {
		t.Fatalf("create isolated MySQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := adminDB.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`").Error; err != nil {
			t.Errorf("drop isolated MySQL database: %v", err)
		}
		if sqlDB, err := adminDB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	testCfg := *cfg
	testCfg.DBName = databaseName
	db, err := store.OpenMySQL(testCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open isolated MySQL database: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
