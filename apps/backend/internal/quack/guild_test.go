package quack_test

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
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
	service := quack.NewGuildService(newMigratedStore(t), fakeDiscordClient{
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
	if !guilds[2].CanManageGuild || !guilds[2].CanManageRules || guilds[2].CanModerate || !guilds[2].QuackInGuild {
		t.Fatalf("expected manage guild with quack present, got %+v", guilds[2])
	}
}

// roleDirectory is a Discord where the user holds roles[guildID] in each
// guild, counting the member lookups ListUserManageableGuilds makes.
type roleDirectory struct {
	fakeDiscordClient
	roles map[string][]string
	// guildRoles are each guild's roles when known; failing guilds' lookups
	// fail.
	guildRoles map[string][]string
	failing    map[string]bool
	mu         sync.Mutex
	lookups    []string
}

func (d *roleDirectory) GuildAuthorization(_ context.Context, guildID, actorID, _ string) (*quack.DiscordGuildAuthorization, error) {
	d.mu.Lock()
	d.lookups = append(d.lookups, guildID)
	d.mu.Unlock()
	if d.failing[guildID] {
		return nil, quack.ErrAuthorizationUnavailable
	}
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: guildID, RoleIDs: d.guildRoles[guildID]},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, RoleIDs: d.roles[guildID]},
	}, nil
}

// TestListUserManageableGuildsLooksUpRolesOnlyWhereNeeded checks that the
// server list reads member roles only for installed guilds whose
// configured staff roles could change the answer, and reports what each
// grants.
func TestListUserManageableGuildsLooksUpRolesOnlyWhereNeeded(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	moderate := uint64(discordgo.PermissionModerateMembers)
	mods := quack.StaffRoles{ModeratorRoleIDs: []string{"mods"}}
	rules := quack.StaffRoles{RulesManagerRoleIDs: []string{"rules"}}
	configured := map[string]quack.StaffRoles{
		"owned": mods, "admin": mods, "role-mod": mods, "not-mod": mods,
		"manager-rules": rules, "rules": rules, "fallback": {}, "plain": {}, "mfa": mods,
		"lookup-fails": mods, "roles-deleted": mods,
	}
	var userGuilds []quack.DiscordUserGuild
	var botGuilds []quack.DiscordBotGuild
	add := func(id string, owner bool, bits uint64, installed, mfa bool) {
		userGuilds = append(userGuilds, quack.DiscordUserGuild{ID: id, Name: id, Owner: owner, Permissions: bits})
		if installed {
			botGuilds = append(botGuilds, quack.DiscordBotGuild{ID: id, Name: id, MFARequired: mfa})
			configureStaffRoles(t, repositories, id, configured[id])
		}
	}
	add("owned", true, 0, true, false)
	add("admin", false, uint64(discordgo.PermissionAdministrator), true, false)
	add("fallback", false, moderate, true, false)
	add("role-mod", false, 0, true, false)
	add("not-mod", false, moderate, true, false)
	add("manager-rules", false, uint64(discordgo.PermissionManageGuild), true, false)
	add("rules", false, 0, true, false)
	add("plain", false, uint64(discordgo.PermissionSendMessages), true, false)
	add("uninstalled", false, moderate, false, false)
	add("mfa", true, 0, true, true)
	add("lookup-fails", false, moderate, true, false)
	add("roles-deleted", false, moderate, true, false)

	directory := &roleDirectory{
		fakeDiscordClient: fakeDiscordClient{userGuilds: userGuilds, botGuilds: botGuilds},
		roles:             map[string][]string{"role-mod": {"mods"}, "not-mod": {"other"}, "rules": {"rules"}},
		guildRoles:        map[string][]string{"roles-deleted": {"roles-deleted", "other"}},
		failing:           map[string]bool{"lookup-fails": true},
	}
	service := quack.NewGuildService(repositories, directory)
	guilds, err := service.ListUserManageableGuilds(ctx, testSession("user-1"))
	if err != nil {
		t.Fatalf("list guilds: %v", err)
	}
	slices.Sort(directory.lookups)
	if want := []string{"lookup-fails", "not-mod", "role-mod", "roles-deleted", "rules"}; !slices.Equal(directory.lookups, want) {
		t.Fatalf("looked up roles in %v, want only %v", directory.lookups, want)
	}

	type flags struct{ moderate, manage, rules, mfa bool }
	got := map[string]flags{}
	for _, guild := range guilds {
		got[guild.DiscordGuildID] = flags{guild.CanModerate, guild.CanManageGuild, guild.CanManageRules, guild.MFARequired}
	}
	want := map[string]flags{
		"owned":         {moderate: true, manage: true, rules: true},
		"admin":         {moderate: true, manage: true, rules: true},
		"fallback":      {moderate: true},
		"role-mod":      {moderate: true},
		"manager-rules": {manage: true, rules: true},
		"rules":         {rules: true},
		"uninstalled":   {moderate: true},
		"mfa":           {mfa: true},
		// A failed lookup falls back to permission bits, and so do
		// moderator roles that were all deleted.
		"lookup-fails":  {moderate: true},
		"roles-deleted": {moderate: true},
	}
	if !maps.Equal(got, want) {
		t.Fatalf("guilds = %+v\nwant     %+v", got, want)
	}

	// Confirming 2FA unlocks the guild that requires it.
	if err := repositories.RecordDiscordUserMFA(ctx, "user-1", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	guilds, err = service.ListUserManageableGuilds(ctx, testSession("user-1"))
	if err != nil {
		t.Fatalf("list guilds: %v", err)
	}
	for _, guild := range guilds {
		if guild.DiscordGuildID == "mfa" && (guild.MFARequired || !guild.CanModerate) {
			t.Fatalf("2FA-confirmed owner still blocked: %+v", guild)
		}
	}
}

// configureStaffRoles installs the Discord guild discordGuildID with roles
// as its staff roles, and returns its internal ID.
func configureStaffRoles(t *testing.T, repositories *store.Store, discordGuildID string, roles quack.StaffRoles) string {
	t.Helper()
	ctx := context.Background()
	result, err := repositories.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		DiscordGuildID: discordGuildID, Name: discordGuildID, OwnerDiscordUserID: "owner", Starter: quack.StarterTemplate(),
	})
	if err != nil {
		t.Fatalf("bootstrap %s: %v", discordGuildID, err)
	}
	settings, err := repositories.GetGuildSettings(ctx, result.Guild.ID)
	if err != nil || settings == nil {
		t.Fatalf("settings for %s: %+v, %v", discordGuildID, settings, err)
	}
	settings.ModeratorRoleIDs, settings.RulesManagerRoleIDs = roles.ModeratorRoleIDs, roles.RulesManagerRoleIDs
	if _, err := repositories.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
		t.Fatalf("configure staff roles for %s: %v", discordGuildID, err)
	}
	return result.Guild.ID
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
	if err := guildContext.Authorize(quack.PermissionActionCaseCreate); !errors.Is(err, quack.ErrAuthorizationDenied) {
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

// insertGuildSettings writes settings as the guild_settings row of a guild
// that was never bootstrapped. Only the channel fields are kept.
func insertGuildSettings(repositories *store.Store, settings quack.GuildSettings) error {
	now := time.Now().UTC()
	return repositories.DB().Table("guild_settings").Create(map[string]any{
		"id": quack.NewID(), "created_at": now, "updated_at": now, "guild_id": settings.GuildID,
		"audit_mirror_channel_discord_id":     settings.AuditMirrorChannelDiscordID,
		"managed_evidence_channel_discord_id": settings.ManagedEvidenceChannelDiscordID,
		"notification_introduction":           "", "notification_footer": "", "starter_policy_notice_pending": false,
	}).Error
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
