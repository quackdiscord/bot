package store_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestActionQueueSnapshot(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	otherID := addGuild(t, s, "guild-2")
	created := createCase(t, s, guildID, []quack.CaseActionExecution{
		{Position: 1, ActionType: quack.ActionTimeoutUser},
		{Position: 2, ActionType: quack.ActionBanUser, Status: quack.ActionExecutionFailed, LastErrorCode: "missing_permissions"},
	}, nil)
	createCase(t, s, otherID, timeout(1), nil)

	snapshot, err := s.ActionQueueSnapshot(ctx, guildID, 10)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[quack.ActionExecutionStatus]int64{}
	for _, row := range snapshot.StatusCounts {
		counts[row.Status] = row.Count
	}
	if counts[quack.ActionExecutionPending] != 1 || counts[quack.ActionExecutionFailed] != 1 {
		t.Errorf("status counts = %+v", snapshot.StatusCounts)
	}
	if oldest := snapshot.OldestPendingOrRetry; oldest == nil || oldest.CaseID != created.Case.ID || oldest.CaseNumber != 1 {
		t.Errorf("oldest = %+v", oldest)
	}
	if len(snapshot.RecentFailures) != 1 || snapshot.RecentFailures[0].LastErrorCode != "missing_permissions" {
		t.Errorf("recent failures = %+v", snapshot.RecentFailures)
	}

	all, err := s.ActionQueueSnapshot(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, row := range all.StatusCounts {
		total += row.Count
	}
	if total != 3 {
		t.Errorf("all-guild snapshot counts %d executions, want 3", total)
	}
}

func TestOperationalMetricSnapshot(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	createCase(t, s, guildID, []quack.CaseActionExecution{
		{Position: 1, ActionType: quack.ActionBanUser, Status: quack.ActionExecutionFailed},
	}, pendingNotification())
	metrics, err := s.OperationalMetricSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		"quack_cases_total":           1,
		"quack_notifications_total":   1,
		"quack_action_failures_total": 1,
		"quack_appeals_total":         0,

		"quack_audit_mirror_deliveries_total": 0,
		"quack_audit_mirror_failures_total":   0,
	}
	for metric, value := range want {
		if got, ok := metrics[metric]; !ok || got != value {
			t.Errorf("%s = %d (reported %v), want %d", metric, got, ok, value)
		}
	}
	if _, ok := metrics["quack_escalation_levels_total"]; ok {
		t.Error("quack_escalation_levels_total is back; it counted cases, not levels")
	}
}
