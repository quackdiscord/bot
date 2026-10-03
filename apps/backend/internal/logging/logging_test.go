package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestStructuredLogsCarryTraceAndHonorLevel(t *testing.T) {
	var output bytes.Buffer
	logger, err := New(&output, false, "info")
	if err != nil {
		t.Fatal(err)
	}
	ctx := quack.ContextWithTrace(context.Background(), "request-1", "correlation-1")
	logger.DebugContext(ctx, "hidden")
	if output.Len() != 0 {
		t.Fatal("debug enabled at info level")
	}
	logger.With("component", "actions").InfoContext(ctx, "Action recorded", "case_id", "case-1")
	var record map[string]any
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"request_id": "request-1", "correlation_id": "correlation-1", "case_id": "case-1", "component": "actions"} {
		if record[key] != want {
			t.Fatalf("%s = %v, want %s", key, record[key], want)
		}
	}
}
