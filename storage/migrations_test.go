package storage_test

import (
	"testing"

	"github.com/quackdiscord/bot/internal/testutil"
)

func TestMigrateSQLiteSmoke(t *testing.T) {
	store := testutil.NewSQLiteStore(t)

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate sqlite schema: %v", err)
	}
}
