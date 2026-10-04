package quack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

type fakeDiscordClient struct {
	userGuilds    []quack.DiscordUserGuild
	botGuilds     []quack.DiscordBotGuild
	botGuild      *quack.DiscordBotGuild
	botErr        error
	authorization *quack.DiscordGuildAuthorization
}

func (f fakeDiscordClient) GuildAuthorization(ctx context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	if f.botErr != nil {
		return nil, f.botErr
	}
	if f.authorization != nil {
		copy := *f.authorization
		return &copy, nil
	}
	if f.botGuild == nil {
		return nil, quack.ErrBotNotInGuild
	}
	actor := quack.DiscordMemberAuthorization{DiscordUserID: actorID}
	for _, guild := range f.userGuilds {
		if guild.ID == guildID {
			actor.Present = true
			actor.PermissionBits = guild.Permissions
			break
		}
	}
	return &quack.DiscordGuildAuthorization{
		Guild: *f.botGuild, Actor: actor,
		Bot: quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 100, Bot: true},
	}, nil
}

func (f fakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	return f.userGuilds, nil
}

func (f fakeDiscordClient) BotGuilds(ctx context.Context) ([]quack.DiscordBotGuild, error) {
	return f.botGuilds, nil
}

func (f fakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*quack.DiscordBotGuild, error) {
	if f.botErr != nil {
		return nil, f.botErr
	}
	return f.botGuild, nil
}

func TestListUserManageableGuilds(t *testing.T) {
	service := quack.NewGuildService(nil, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{
			{ID: "owned", Name: "Owned", Owner: true, Permissions: 0},
			{ID: "admin", Name: "Admin", Permissions: uint64(discordgo.PermissionAdministrator)},
			{ID: "manage", Name: "Manage", Permissions: uint64(discordgo.PermissionManageGuild)},
			{ID: "member", Name: "Member", Permissions: uint64(discordgo.PermissionSendMessages)},
		},
		botGuilds: []quack.DiscordBotGuild{
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
	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{ID: "guild-1", Owner: true, Permissions: 0}},
		botGuild:   &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "user-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve staff context: %v", err)
	}

	if !guildContext.IsAdmin || guildContext.IsModerator {
		t.Fatalf("expected owner to classify as admin, got admin=%v moderator=%v", guildContext.IsAdmin, guildContext.IsModerator)
	}
	for action, allowed := range guildContext.Permissions {
		if !allowed {
			t.Fatalf("expected owner to be allowed for %s", action)
		}
	}
}

func TestResolveStaffContextEvaluatesPermissionBits(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: uint64(discordgo.PermissionModerateMembers),
		}},
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve staff context: %v", err)
	}

	if !guildContext.IsModerator || guildContext.IsAdmin {
		t.Fatalf("expected discord moderate members permission to classify as moderator, got admin=%v moderator=%v", guildContext.IsAdmin, guildContext.IsModerator)
	}
	if !guildContext.Can(quack.PermissionActionCaseCreate) {
		t.Fatalf("expected moderate members permission to allow case.create")
	}
	if guildContext.Can(quack.PermissionActionCaseTemplateWrite) {
		t.Fatalf("expected moderate members permission not to allow case_template.write")
	}
	if !guildContext.Can(quack.PermissionActionAuditRead) {
		t.Fatalf("expected moderate members permission to allow audit.read")
	}

}

func TestResolveStaffContextRejectsMemberActions(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: uint64(discordgo.PermissionSendMessages),
		}},
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve staff context: %v", err)
	}

	if guildContext.IsAdmin || guildContext.IsModerator {
		t.Fatalf("expected normal member to have no staff role, got admin=%v moderator=%v", guildContext.IsAdmin, guildContext.IsModerator)
	}
	if guildContext.Can(quack.PermissionActionCaseCreate) || guildContext.Can(quack.PermissionActionCaseTemplateWrite) {
		t.Fatalf("expected normal member without moderation capabilities")
	}
}

func TestResolveStaffContextRejectsMissingUserGuildMembership(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{ID: "other-guild", Permissions: uint64(discordgo.PermissionModerateMembers)}},
		botGuild:   &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	guildContext, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if err != nil {
		t.Fatalf("resolve former staff context: %v", err)
	}
	if err := service.Authorize(context.Background(), guildContext, quack.PermissionActionCaseCreate, quack.AuditSourceAPI); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("expected live membership denial, got %v", err)
	}
}

func TestResolveStaffContextRejectsBotNotInGuild(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{ID: "guild-1", Permissions: uint64(discordgo.PermissionModerateMembers)}},
		botErr:     quack.ErrBotNotInGuild,
	})

	_, err := service.ResolveStaffContext(context.Background(), testSession("user-1"), "guild-1")
	if !errors.Is(err, quack.ErrBotNotInGuild) {
		t.Fatalf("expected ErrBotNotInGuild, got %v", err)
	}
}

func TestResolveDiscordStaffContextEvaluatesInteractionPermissions(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
		authorization: &quack.DiscordGuildAuthorization{
			Guild: quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
			Actor: quack.DiscordMemberAuthorization{DiscordUserID: "user-1", DisplayName: "Live Command User", Present: true, PermissionBits: uint64(discordgo.PermissionModerateMembers)},
			Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true},
		},
	})

	guildContext, err := service.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "user-1",
		DisplayName:    "Command User",
		PermissionBits: uint64(discordgo.PermissionModerateMembers),
	})
	if err != nil {
		t.Fatalf("resolve discord staff context: %v", err)
	}

	if guildContext.Staff.LastKnownDisplayName != "Live Command User" {
		t.Fatalf("expected display name to be stored, got %q", guildContext.Staff.LastKnownDisplayName)
	}
	if !guildContext.Can(quack.PermissionActionCaseCreate) {
		t.Fatalf("expected moderate members permission to allow case.create")
	}
	if guildContext.Can(quack.PermissionActionCaseTemplateWrite) {
		t.Fatalf("expected moderate members permission not to allow template writes")
	}
}

func TestResolveDiscordStaffContextOwnerBypassAllowsAllActions(t *testing.T) {
	store := newMigratedStore(t)
	service := quack.NewGuildService(store, fakeDiscordClient{
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
		authorization: &quack.DiscordGuildAuthorization{
			Guild: quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
			Actor: quack.DiscordMemberAuthorization{DiscordUserID: "owner-1", Present: true},
			Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true},
		},
	})

	guildContext, err := service.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
		DiscordGuildID: "guild-1",
		DiscordUserID:  "owner-1",
		DisplayName:    "Owner",
	})
	if err != nil {
		t.Fatalf("resolve discord staff context: %v", err)
	}

	if !guildContext.IsAdmin || guildContext.IsModerator {
		t.Fatalf("expected owner to classify as admin, got admin=%v moderator=%v", guildContext.IsAdmin, guildContext.IsModerator)
	}
	for action, allowed := range guildContext.Permissions {
		if !allowed {
			t.Fatalf("expected owner to be allowed for %s", action)
		}
	}
}

func newMigratedStore(t *testing.T) *store.Store {
	t.Helper()

	store := testutil.NewSQLiteStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	return store
}

func testSession(discordUserID string) *quack.AuthSession {
	now := time.Now().UTC()
	return &quack.AuthSession{
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
