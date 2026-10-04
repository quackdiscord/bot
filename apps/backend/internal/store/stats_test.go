package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestDeriveStaffStatistics(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	otherID := addGuild(t, s, "guild-2")
	template := createTemplate(t, s, guildID, "spam")
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)

	templated := newCase(guildID, &template.Template.ID)
	first, err := s.CreateCase(ctx, quack.CreateCaseParams{Case: templated, Event: newCaseEvent(), ActionExecutions: timeout(0)})
	if err != nil {
		t.Fatal(err)
	}
	second := createCase(t, s, guildID, nil, nil)
	outside := createCase(t, s, guildID, nil, nil)
	createCase(t, s, otherID, timeout(0), nil)
	backdate(t, s, "cases", first.Case.ID, day.Add(2*time.Hour))
	backdate(t, s, "case_action_executions", first.ActionExecutions[0].ID, day.Add(2*time.Hour))
	backdate(t, s, "cases", second.Case.ID, day.Add(26*time.Hour))
	backdate(t, s, "cases", outside.Case.ID, day.Add(-time.Hour))

	stats, err := s.DeriveStaffStatistics(ctx, quack.StaffStatisticsParams{GuildID: guildID, From: day, To: day.Add(48 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if stats.CaseTotal != 2 || stats.ActionTotal != 1 || stats.AppealTotal != 0 {
		t.Fatalf("totals = cases %d, actions %d, appeals %d", stats.CaseTotal, stats.ActionTotal, stats.AppealTotal)
	}
	assertBuckets(t, "cases by day", stats.CasesByDay, map[string]int64{"2026-03-10": 1, "2026-03-11": 1})
	assertBuckets(t, "cases by template", stats.CasesByTemplate, map[string]int64{template.Template.ID: 1, "historical_or_deleted": 1})
	assertBuckets(t, "cases by validity", stats.CasesByValidity, map[string]int64{"valid": 2})
	assertBuckets(t, "actions by type", stats.ActionsByType, map[string]int64{"timeout_user": 1})
	assertBuckets(t, "actions by result", stats.ActionsByResult, map[string]int64{"pending": 1})
	if stats.AppealsByDay == nil || len(stats.AppealsByDay) != 0 {
		t.Errorf("appeals by day = %#v, want an empty slice", stats.AppealsByDay)
	}

	if _, err := s.DeriveStaffStatistics(ctx, quack.StaffStatisticsParams{GuildID: guildID, From: day, To: day}); err == nil {
		t.Error("empty range accepted")
	}
}

func assertBuckets(t *testing.T, name string, got []quack.StatisticBucket, want map[string]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %+v, want %v", name, got, want)
		return
	}
	for i, bucket := range got {
		if want[bucket.Key] != bucket.Count || (i > 0 && got[i-1].Key >= bucket.Key) {
			t.Errorf("%s = %+v, want %v in key order", name, got, want)
			return
		}
	}
}

// backdate moves a row's created_at.
func backdate(t *testing.T, s *store.Store, table, id string, at time.Time) {
	t.Helper()
	if err := s.DB().Table(table).Where("id = ?", id).Update("created_at", at).Error; err != nil {
		t.Fatalf("backdate %s: %v", table, err)
	}
}
