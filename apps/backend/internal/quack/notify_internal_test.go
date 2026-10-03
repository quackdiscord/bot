package quack

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestAppealNotificationErrorCodeReadsClassification(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("appeal_dm: %w", DiscordError{Code: "appeal_dm_permission_or_hierarchy_denied"}), "discord_forbidden"},
		{fmt.Errorf("appeal_dm: %w", DiscordError{Code: "appeal_dm_rate_limited"}), "discord_rate_limited"},
		{fmt.Errorf("appeal_dm: %w", context.DeadlineExceeded), "discord_timeout"},
		{DiscordError{Code: "appeal_dm_network_error"}, "discord_delivery_failed"},
		// Text no longer decides the code: "private" contains "rate".
		{errors.New("destination must be a private text channel in this guild"), "discord_delivery_failed"},
		{errors.New("missing permission"), "discord_delivery_failed"},
	} {
		if got := appealNotificationErrorCode(test.err); got != test.want {
			t.Errorf("appealNotificationErrorCode(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}
