package discord

import (
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
)

// TestReversalReceiptReportsStoredStatus keeps repeated reversals truthful
// after the original removal finishes or needs staff review.
func TestReversalReceiptReportsStoredStatus(t *testing.T) {
	for _, test := range []struct {
		status quack.ActionExecutionStatus
		want   string
	}{
		{quack.ActionExecutionPending, "Remove timeout queued."},
		{quack.ActionExecutionRunning, "Remove timeout in progress."},
		{quack.ActionExecutionSucceeded, "Member is no longer timed out."},
		{quack.ActionExecutionFailed, "Staff review needed."},
		{quack.ActionExecutionCancelled, "Remove timeout cancelled."},
	} {
		t.Run(string(test.status), func(t *testing.T) {
			message := reversalReceipt(&quack.CaseActionExecution{ActionType: quack.ActionRemoveTimeout, Status: test.status})
			if !strings.Contains(message.Content, test.want) || !strings.Contains(message.Content, "original action stays") {
				t.Fatalf("receipt = %s; want %s", message.Content, test.want)
			}
			if test.status != quack.ActionExecutionPending && strings.Contains(message.Content, "queued") {
				t.Fatal("reported another queued reversal", message.Content)
			}
		})
	}
}
