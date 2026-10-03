package quack

import (
	"context"
	"errors"
	"testing"
)

func TestUnclassifiedFailuresRequireReviewAndHideDetails(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded, errors.New("secret response payload")} {
		t.Run(err.Error(), func(t *testing.T) {
			result := resultFromError(err)
			if result.Retryable || !result.OutcomeUncertain || result.Error == err.Error() {
				t.Fatalf("unclassified error must require review: %+v", result)
			}
		})
	}
}

func TestClassifiedFailuresKeepTheirClassification(t *testing.T) {
	tests := []struct {
		name string
		err  DiscordError
	}{
		{"retryable", DiscordError{Code: "rate_limited", Message: "slow down", Retryable: true}},
		{"permanent", DiscordError{Code: "missing_permissions"}},
		{"uncertain", DiscordError{Code: "network", OutcomeUncertain: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resultFromError(tt.err)
			if result.ErrorCode != tt.err.Code || result.Retryable != tt.err.Retryable || result.OutcomeUncertain != tt.err.OutcomeUncertain || result.Error != tt.err.Error() {
				t.Fatalf("resultFromError(%+v) = %+v", tt.err, result)
			}
		})
	}
}

func TestDecodeActionConfigRejectsInexactValues(t *testing.T) {
	tests := []struct {
		body string
		want actionConfig
	}{
		{`{"duration_seconds":60}`, actionConfig{DurationSeconds: 60}},
		{`{"delete_message_seconds":86400}`, actionConfig{DeleteMessageSeconds: 86400}},
		{`{"duration_seconds":1.5}`, actionConfig{}},
		{`{"duration_seconds":1e400}`, actionConfig{}},
		{`{"duration_seconds":"60"}`, actionConfig{}},
		{`not json`, actionConfig{}},
		{``, actionConfig{}},
	}
	for _, tt := range tests {
		if got := decodeActionConfig(tt.body); got != tt.want {
			t.Errorf("decodeActionConfig(%q) = %+v, want %+v", tt.body, got, tt.want)
		}
	}
}

func TestShouldRetryOnlyWhenSafe(t *testing.T) {
	retryable := attemptResult{Retryable: true, ErrorCode: "rate_limited", Error: "slow down"}
	tests := []struct {
		name      string
		execution CaseActionExecution
		result    attemptResult
		want      bool
	}{
		{"safe with budget", CaseActionExecution{SafeForRetry: true, AttemptCount: 1, MaxRetries: 1}, retryable, true},
		{"budget spent", CaseActionExecution{SafeForRetry: true, AttemptCount: 2, MaxRetries: 1}, retryable, false},
		{"reversal", CaseActionExecution{SafeForRetry: false, AttemptCount: 1, MaxRetries: 3}, retryable, false},
		{"uncertain outcome", CaseActionExecution{SafeForRetry: true, AttemptCount: 1, MaxRetries: 3}, attemptResult{Retryable: true, OutcomeUncertain: true, Error: "x"}, false},
		{"permanent", CaseActionExecution{SafeForRetry: true, AttemptCount: 1, MaxRetries: 3}, attemptResult{Error: "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldRetry(tt.execution, tt.result); got != tt.want {
				t.Fatalf("shouldRetry() = %v, want %v", got, tt.want)
			}
		})
	}
}
