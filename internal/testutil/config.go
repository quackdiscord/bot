package testutil

import (
	"testing"

	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/structs"
)

func SetTestConfig(t testing.TB) {
	t.Helper()

	previous := lib.Config
	lib.Config = &structs.Config{
		Environment: "test",
		Auth: structs.AuthConfig{
			SessionCookieName: "quack_test_session",
			SessionTTLHours:   1,
			StateTTLMinutes:   10,
			PostLoginRedirect: "/",
			CookieSecure:      false,
		},
	}

	t.Cleanup(func() {
		lib.Config = previous
	})
}
