package store_test

import (
	"context"
	"github.com/quackdiscord/bot/internal/quack"
	"testing"

	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestStaffUpsertRefreshesActivity(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	migrateStore(t, store)

	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID:     "100",
		Name:               "Quack Test",
		OwnerDiscordUserID: "200",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	staff, err := store.UpsertStaffMember(ctx, quack.UpsertStaffMemberParams{
		GuildID:                guild.ID,
		DiscordUserID:          "300",
		LastSeenPermissionBits: 1,
		LastKnownDisplayName:   "Old Name",
	})
	if err != nil {
		t.Fatalf("create staff: %v", err)
	}
	if staff.LastActiveAt == nil {
		t.Fatalf("expected last active time to be set")
	}

	updated, err := store.UpsertStaffMember(ctx, quack.UpsertStaffMemberParams{
		GuildID:                guild.ID,
		DiscordUserID:          "300",
		LastSeenPermissionBits: 64,
		LastKnownDisplayName:   "New Name",
	})
	if err != nil {
		t.Fatalf("update staff: %v", err)
	}

	if updated.ID != staff.ID {
		t.Fatalf("expected staff upsert to preserve id")
	}
	if updated.LastSeenPermissionBits != 64 {
		t.Fatalf("expected permission bits to refresh")
	}
	if updated.LastKnownDisplayName != "New Name" {
		t.Fatalf("expected display name to refresh")
	}
	if updated.LastActiveAt == nil {
		t.Fatalf("expected last active time to refresh")
	}
}

func migrateStore(t *testing.T, store *storage.Store) {
	t.Helper()

	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
}
