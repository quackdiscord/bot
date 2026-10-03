// Package testutil builds the stores and fixtures that tests in several
// packages share.
package testutil

import "testing"

// SetTestConfig is a no-op kept for existing callers. Configuration is now
// passed explicitly, so tests have no global state to set.
func SetTestConfig(t testing.TB) {
	t.Helper()
}
