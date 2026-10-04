package store_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/internal/v4import"
)

func TestV4ImportIsHistoricalIdempotentAndReversible(t *testing.T) {
	ctx := context.Background()
	s, guildID := importStore(t)
	fixture := historicalFixture(t)
	importer := v4import.New(s)
	run := func(input []byte, dryRun bool) (*v4import.Report, error) {
		return importer.Import(ctx, "fixture", guildID, "operator", bytes.NewReader(input), dryRun)
	}

	dry, err := run(fixture, true)
	if err != nil || dry.Created != 0 || len(dry.Decisions) != 4 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if cases, _ := s.ListCases(ctx, guildID); len(cases) != 0 {
		t.Fatalf("dry run wrote %d cases", len(cases))
	}

	report, err := run(fixture, false)
	if err != nil || report.Created != 4 {
		t.Fatalf("import = %+v, %v", report, err)
	}
	cases, err := s.ListCases(ctx, guildID)
	if err != nil || len(cases) != 4 {
		t.Fatalf("cases = %d, %v", len(cases), err)
	}
	for _, c := range cases {
		if c.Source != quack.CaseSourceV4Import || c.TemplateID != nil || c.TemplateVersion != 0 ||
			!strings.Contains(c.MetadataJSON, `"historical":true`) {
			t.Fatalf("imported case is not historical: %+v", c)
		}
		executions, _ := s.ListCaseActionExecutions(ctx, c.ID)
		notification, _ := s.GetCaseNotification(ctx, c.ID)
		if len(executions) != 0 || notification != nil {
			t.Fatalf("import queued work for case %s", c.ID)
		}
	}
	member, err := s.ListCasesFiltered(ctx, quack.ListCasesParams{GuildID: guildID, TargetDiscordUserID: "member-departed"})
	if err != nil || member.Total != 1 {
		t.Fatalf("member history = %+v, %v", member, err)
	}

	repeat, err := run(fixture, false)
	if err != nil || repeat.Created != 0 || repeat.AlreadyImported != 4 {
		t.Fatalf("repeat import = %+v, %v", repeat, err)
	}
	for i, decision := range repeat.Decisions {
		if decision.TargetCaseNumber != report.Decisions[i].TargetCaseNumber {
			t.Fatalf("repeat decision %d maps to case %d, first import to %d",
				i, decision.TargetCaseNumber, report.Decisions[i].TargetCaseNumber)
		}
	}
	changed := bytes.Replace(fixture, []byte("Historical warning"), []byte("Changed warning"), 1)
	if _, err := run(changed, false); !errors.Is(err, v4import.ErrSourceCollision) {
		t.Fatalf("changed source row = %v, want ErrSourceCollision", err)
	}

	if err := importer.Rollback(ctx, guildID, report.BatchID, "operator"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if cases, _ := s.ListCases(ctx, guildID); len(cases) != 0 {
		t.Fatalf("rollback left %d cases", len(cases))
	}
	audits, err := s.ListAuditLogEntriesFiltered(ctx, quack.ListAuditLogEntriesParams{GuildID: guildID, Action: "v4_import.rollback"})
	if err != nil || audits.Total != 1 {
		t.Fatalf("rollback audits = %+v, %v", audits, err)
	}
}

func TestV4ImportRemapsTakenCaseNumbers(t *testing.T) {
	ctx := context.Background()
	s, guildID := importStore(t)
	createCase(t, s, guildID, nil, nil)
	fixture := historicalFixture(t)
	report, err := v4import.New(s).Import(ctx, "fixture", guildID, "operator", bytes.NewReader(fixture), false)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[uint64]bool{1: true}
	for _, decision := range report.Decisions {
		if seen[decision.TargetCaseNumber] {
			t.Fatalf("case number %d assigned twice: %+v", decision.TargetCaseNumber, report.Decisions)
		}
		seen[decision.TargetCaseNumber] = true
	}
}

func TestV4RollbackRefusesTouchedCases(t *testing.T) {
	ctx := context.Background()
	s, guildID := importStore(t)
	fixture := historicalFixture(t)
	importer := v4import.New(s)
	report, err := importer.Import(ctx, "fixture", guildID, "operator", bytes.NewReader(fixture), false)
	if err != nil {
		t.Fatal(err)
	}
	createAppeal(t, s, guildID, report.Decisions[0].TargetCaseID)
	if err := importer.Rollback(ctx, guildID, report.BatchID, "operator"); err == nil {
		t.Fatal("rolled back a batch whose case has an appeal")
	}
	if cases, _ := s.ListCases(ctx, guildID); len(cases) != 4 {
		t.Fatalf("refused rollback still removed cases: %d left", len(cases))
	}
}

// historicalFixture reads the sample v4 export: four cases for importStore's
// guild.
func historicalFixture(t *testing.T) []byte {
	t.Helper()
	fixture, err := os.ReadFile("../v4import/testdata/historical_cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

// importStore returns a store with the guild the v4 fixture belongs to.
func importStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	s := testutil.NewSQLiteStore(t)
	const guildID = "01J40000000000000000000001"
	now := time.Now().UTC()
	guild := quack.Guild{
		ULIDModel:          quack.ULIDModel{ID: guildID, CreatedAt: now, UpdatedAt: now},
		DiscordGuildID:     "discord-import-guild",
		Name:               "Import guild",
		OwnerDiscordUserID: "owner",
		IsActive:           true,
	}
	if err := s.DB().Create(&guild).Error; err != nil {
		t.Fatal(err)
	}
	return s, guildID
}
