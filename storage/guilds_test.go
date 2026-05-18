package storage_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestGuildBootstrapCreatesSettingsAndPolicies(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	migrateStore(t, store)

	guild, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "100",
		Name:               "Quack Test",
		OwnerDiscordUserID: "200",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	settings, err := store.EnsureGuildSettings(ctx, guild.ID)
	if err != nil {
		t.Fatalf("ensure guild settings: %v", err)
	}
	if settings.FeatureFlagsJSON != "{}" || settings.GuildModerationConfigJSON != "{}" {
		t.Fatalf("expected default settings json fields to be empty objects")
	}

	policies, err := store.EnsureDefaultGuildPermissionPolicies(ctx, guild.ID)
	if err != nil {
		t.Fatalf("ensure default policies: %v", err)
	}
	if len(policies) != len(storage.DefaultGuildPermissionPolicies(guild.ID)) {
		t.Fatalf("expected %d policies, got %d", len(storage.DefaultGuildPermissionPolicies(guild.ID)), len(policies))
	}
	if requirePolicy(t, policies, structs.PermissionActionCaseCreate).MinimumPermissionBits == 0 {
		t.Fatalf("expected case create to require discord permissions")
	}
	if requirePolicy(t, policies, structs.PermissionActionCaseTemplateWrite).MinimumPermissionBits == 0 {
		t.Fatalf("expected template write to require discord permissions")
	}

	policiesAgain, err := store.EnsureDefaultGuildPermissionPolicies(ctx, guild.ID)
	if err != nil {
		t.Fatalf("ensure default policies again: %v", err)
	}
	if len(policiesAgain) != len(policies) {
		t.Fatalf("expected no duplicate policies, got %d then %d", len(policies), len(policiesAgain))
	}
}

func TestStaffUpsertRefreshesActivity(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	migrateStore(t, store)

	guild, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "100",
		Name:               "Quack Test",
		OwnerDiscordUserID: "200",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}

	staff, err := store.UpsertStaffMember(ctx, storage.UpsertStaffMemberParams{
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

	updated, err := store.UpsertStaffMember(ctx, storage.UpsertStaffMemberParams{
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

func requirePolicy(t *testing.T, policies []structs.GuildPermissionPolicy, action structs.PermissionAction) structs.GuildPermissionPolicy {
	t.Helper()

	for _, policy := range policies {
		if policy.Action == action {
			return policy
		}
	}

	t.Fatalf("missing policy for action %s", action)
	return structs.GuildPermissionPolicy{}
}
