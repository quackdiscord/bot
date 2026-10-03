package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestFailedActionListHidesInternals(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	body, err := json.Marshal(newFailedActionList(&quack.FailedCaseActionResult{
		Executions: []quack.CaseActionExecution{{
			ULIDModel:      quack.ULIDModel{ID: "execution-1", CreatedAt: now, UpdatedAt: now},
			CaseID:         "case-1",
			ActionType:     quack.ActionBanUser,
			Status:         quack.ActionExecutionFailed,
			AttemptCount:   2,
			MaxRetries:     3,
			SafeForRetry:   true,
			LastErrorCode:  "discord_forbidden",
			LastError:      "Discord rejected the action",
			IdempotencyKey: "internal-key",
			LeaseToken:     "internal-lease",
		}},
		Total: 1,
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded struct {
		Executions []map[string]any `json:"executions"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Executions) != 1 {
		t.Fatalf("body = %s (err %v)", body, err)
	}
	action := decoded.Executions[0]
	if action["id"] != "execution-1" || action["case_id"] != "case-1" || action["action_type"] != string(quack.ActionBanUser) {
		t.Fatalf("action = %+v", action)
	}
	for _, field := range []string{"CaseID", "IdempotencyKey", "LeaseToken", "idempotency_key", "lease_token"} {
		if _, ok := action[field]; ok {
			t.Errorf("internal field %q leaked", field)
		}
	}
}

func TestFailedActionListIsNeverNull(t *testing.T) {
	for _, result := range []*quack.FailedCaseActionResult{nil, {}} {
		body, err := json.Marshal(newFailedActionList(result))
		if err != nil || string(body) != `{"executions":[],"total":0}` {
			t.Fatalf("body = %s (err %v)", body, err)
		}
	}
}

// TestReversalResponseHidesWorkerState keeps lease and snapshot internals,
// and Go field names, out of the queued reversal the API returns.
func TestReversalResponseHidesWorkerState(t *testing.T) {
	body, err := json.Marshal(actionEnvelope{Action: quack.CaseActionExecution{
		ULIDModel:          quack.ULIDModel{ID: "execution-1"},
		CaseID:             "case-1",
		ActionType:         quack.ActionUnbanUser,
		Status:             quack.ActionExecutionPending,
		LeaseToken:         "internal-lease",
		ConfigSnapshotJSON: `{"secret":true}`,
	}.Response()})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Action map[string]any `json:"action"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Action["id"] != "execution-1" || decoded.Action["action_type"] != string(quack.ActionUnbanUser) {
		t.Fatalf("action = %s", body)
	}
	for _, field := range []string{"ID", "LeaseToken", "lease_token", "ConfigSnapshotJSON", "config_snapshot"} {
		if _, ok := decoded.Action[field]; ok {
			t.Errorf("internal field %q leaked: %s", field, body)
		}
	}
}
