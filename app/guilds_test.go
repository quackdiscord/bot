package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

type fakeDiscordClient struct {
	userGuilds []app.DiscordUserGuild
	botGuilds  []app.DiscordBotGuild
	botGuild   *app.DiscordBotGuild
	botErr     error
}

func (f fakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]app.DiscordUserGuild, error) {
	return f.userGuilds, nil
}

func (f fakeDiscordClient) BotGuilds(ctx context.Context) ([]app.DiscordBotGuild, error) {
	return f.botGuilds, nil
}

func (f fakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*app.DiscordBotGuild, error) {
	if f.botErr != nil {
		return nil, f.botErr
	}
	return f.botGuild, nil
}

func TestListUserManageableGuilds(t *testing.T) {
	service := app.NewGuildService(nil, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{
			{ID: "owned", Name: "Owned", Owner: true, Permissions: 0},
			{ID: "admin", Name: "Admin", Permissions: uint64(discordgo.PermissionAdministrator)},
			{ID: "manage", Name: "Manage", Permissions: uint64(discordgo.PermissionManageGuild)},
			{ID: "member", Name: "Member", Permissions: uint64(discordgo.PermissionSendMessages)},
		},
		botGuilds: []app.DiscordBotGuild{
			{ID: "owned", Name: "Owned"},
			{ID: "manage", Name: "Manage"},
		},
	})

	guilds, err := service.ListUserManageableGuilds(context.Background(), testSession("user-1"))
	if err != nil {
		t.Fatalf("list manageable guilds: %v", err)
	}

	if len(guilds) != 3 {
		t.Fatalf("expected 3 manageable guilds, got %+v", guilds)
	}
	if !guilds[0].IsOwner || !guilds[0].QuackInGuild {
		t.Fatalf("expected owned guild with quack present, got %+v", guilds[0])
	}
	if !guilds[1].IsAdministrator || guilds[1].QuackInGuild {
		t.Fatalf("expected admin guild without quack present, got %+v", guilds[1])
	}
	if !guilds[2].CanManageGuild || !guilds[2].QuackInGuild {
		t.Fatalf("expected manage guild with quack present, got %+v", guilds[2])
	}
}

func TestResolveStaffContextOwnerBypassAllowsAllActions(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{ID: "guild-1", Owner: true, Permissions: 0}},
		botGuild:   &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve staff context: %v", err)
	}

	for action, allowed := range guildContext.Permissions {
		if !allowed {
			t.Fatalf("expected owner to be allowed for %s", action)
		}
	}
}

func TestResolveStaffContextEvaluatesPermissionBits(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: uint64(discordgo.PermissionModerateMembers),
		}},
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve staff context: %v", err)
	}

	if !guildContext.Can(structs.PermissionActionCaseCreate) {
		t.Fatalf("expected moderate members permission to allow case.create")
	}
	if guildContext.Can(structs.PermissionActionCaseTemplateWrite) {
		t.Fatalf("expected moderate members permission not to allow case_template.write")
	}
	if guildContext.Can(structs.PermissionActionAuditRead) {
		t.Fatalf("expected moderate members permission not to allow audit.read")
	}
}

func TestResolveStaffContextRejectsMissingUserGuildMembership(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{ID: "other-guild", Permissions: uint64(discordgo.PermissionModerateMembers)}},
		botGuild:   &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	_, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if !errors.Is(err, app.ErrUserNotInGuild) {
		t.Fatalf("expected ErrUserNotInGuild, got %v", err)
	}
}

func TestResolveStaffContextRejectsBotNotInGuild(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{ID: "guild-1", Permissions: uint64(discordgo.PermissionModerateMembers)}},
		botErr:     app.ErrBotNotInGuild,
	})

	_, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if !errors.Is(err, app.ErrBotNotInGuild) {
		t.Fatalf("expected ErrBotNotInGuild, got %v", err)
	}
}

func TestResolveStaffContextRejectsDisabledStaff(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guild, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	staff, err := store.UpsertStaffMember(ctx, storage.UpsertStaffMemberParams{
		GuildID:                guild.ID,
		DiscordUserID:          "user-1",
		LastSeenPermissionBits: uint64(discordgo.PermissionModerateMembers),
		LastKnownDisplayName:   "User",
	})
	if err != nil {
		t.Fatalf("upsert staff: %v", err)
	}
	disabledAt := time.Now().UTC()
	if err := store.DB().Model(&structs.StaffMember{}).Where("id = ?", staff.ID).Update("disabled_at", disabledAt).Error; err != nil {
		t.Fatalf("disable staff: %v", err)
	}

	service := app.NewGuildService(store, fakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{ID: "guild-1", Permissions: uint64(discordgo.PermissionModerateMembers)}},
		botGuild:   &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	_, err = service.ResolveStaffContext(ctx, testSession("user-1"), "guild-1")
	if !errors.Is(err, app.ErrStaffDisabled) {
		t.Fatalf("expected ErrStaffDisabled, got %v", err)
	}
}

func TestResolveDiscordStaffContextEvaluatesInteractionPermissions(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveDiscordStaffContext(context.Background(), app.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "user-1",
		DisplayName:    "Command User",
		PermissionBits: uint64(discordgo.PermissionModerateMembers),
	})
	if err != nil {
		t.Fatalf("resolve discord staff context: %v", err)
	}

	if guildContext.Staff.LastKnownDisplayName != "Command User" {
		t.Fatalf("expected display name to be stored, got %q", guildContext.Staff.LastKnownDisplayName)
	}
	if !guildContext.Can(structs.PermissionActionCaseCreate) {
		t.Fatalf("expected moderate members permission to allow case.create")
	}
	if guildContext.Can(structs.PermissionActionCaseTemplateWrite) {
		t.Fatalf("expected moderate members permission not to allow template writes")
	}
}

func TestResolveDiscordStaffContextOwnerBypassAllowsAllActions(t *testing.T) {
	store := newMigratedStore(t)
	service := app.NewGuildService(store, fakeDiscordClient{
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveDiscordStaffContext(context.Background(), app.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve discord staff context: %v", err)
	}

	for action, allowed := range guildContext.Permissions {
		if !allowed {
			t.Fatalf("expected owner to be allowed for %s", action)
		}
	}
}

func TestResolveDiscordStaffContextRejectsDisabledStaff(t *testing.T) {
	ctx := context.Background()
	store := newMigratedStore(t)
	guild, err := store.UpsertGuild(ctx, storage.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	staff, err := store.UpsertStaffMember(ctx, storage.UpsertStaffMemberParams{
		GuildID:                guild.ID,
		DiscordUserID:          "user-1",
		LastSeenPermissionBits: uint64(discordgo.PermissionModerateMembers),
		LastKnownDisplayName:   "User",
	})
	if err != nil {
		t.Fatalf("upsert staff: %v", err)
	}
	disabledAt := time.Now().UTC()
	if err := store.DB().Model(&structs.StaffMember{}).Where("id = ?", staff.ID).Update("disabled_at", disabledAt).Error; err != nil {
		t.Fatalf("disable staff: %v", err)
	}

	service := app.NewGuildService(store, fakeDiscordClient{
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	_, err = service.ResolveDiscordStaffContext(ctx, app.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "user-1",
		DisplayName:    "User",
		PermissionBits: uint64(discordgo.PermissionModerateMembers),
	})
	if !errors.Is(err, app.ErrStaffDisabled) {
		t.Fatalf("expected ErrStaffDisabled, got %v", err)
	}
}

func newMigratedStore(t *testing.T) *storage.Store {
	t.Helper()

	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	return store
}

func testSession(discordUserID string) *structs.AuthSession {
	now := time.Now().UTC()
	return &structs.AuthSession{
		ID:               "session-1",
		DiscordUserID:    discordUserID,
		Username:         "user",
		GlobalName:       "User",
		AccessToken:      "token",
		SessionExpiresAt: now.Add(time.Hour),
		CreatedAt:        now,
		LastSeenAt:       now,
	}
}
