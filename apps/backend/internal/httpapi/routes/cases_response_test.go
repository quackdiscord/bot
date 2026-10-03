package routes

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestFailedActionListResponseUsesPublicSnakeCaseShape(t *testing.T) {
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	result := &quack.FailedCaseActionResult{
		Executions: []quack.CaseActionExecution{
			{
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
			},
		},
		Total: 1,
	}

	body, err := json.Marshal(failedActionListResponseFromModel(result))
	if err != nil {
		t.Fatalf("marshal failed action response: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode failed action response: %v", err)
	}
	executions, ok := decoded["executions"].([]any)
	if !ok || len(executions) != 1 {
		t.Fatalf("expected one execution array item, got %s", body)
	}
	action, ok := executions[0].(map[string]any)
	if !ok {
		t.Fatalf("expected execution object, got %T", executions[0])
	}
	if action["id"] != "execution-1" || action["case_id"] != "case-1" || action["action_type"] != string(quack.ActionBanUser) {
		t.Fatalf("unexpected public action fields: %+v", action)
	}
	for _, internalField := range []string{"CaseID", "IdempotencyKey", "LeaseToken", "idempotency_key", "lease_token"} {
		if _, exists := action[internalField]; exists {
			t.Fatalf("internal field %q leaked in response: %+v", internalField, action)
		}
	}
}

func TestFailedActionListResponseUsesEmptyArray(t *testing.T) {
	body, err := json.Marshal(failedActionListResponseFromModel(&quack.FailedCaseActionResult{}))
	if err != nil {
		t.Fatalf("marshal empty failed action response: %v", err)
	}
	if string(body) != `{"executions":[],"total":0}` {
		t.Fatalf("expected stable empty response, got %s", body)
	}
}
