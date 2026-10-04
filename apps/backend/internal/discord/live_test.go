package discord

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// liveBot returns a bot whose gateway state holds one guild with an owner, a
// moderator, the bot, and a moderator role, as after READY and GUILD_CREATE.
// REST calls are counted and answered by serve.
func liveBot(t *testing.T, serve func(*http.Request) (*http.Response, error)) (*Bot, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return serve(request)
	})
	bot.live.track(bot.Session, &discordgo.Ready{})
	joined := time.Now().Add(-time.Hour)
	member := func(id string, roles ...string) *discordgo.Member {
		return &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: id, Username: id}, Roles: roles, JoinedAt: joined}
	}
	err := bot.Session.State.GuildAdd(&discordgo.Guild{
		ID: "guild", Name: "Guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Permissions: discordgo.PermissionViewChannel},
			{ID: "moderator", Position: 5, Permissions: discordgo.PermissionModerateMembers},
			{ID: "quack", Position: 10, Permissions: discordgo.PermissionModerateMembers | discordgo.PermissionBanMembers},
		},
		Members: []*discordgo.Member{member("owner"), member("mod", "moderator"), member("bot", "quack")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bot, &calls
}

// gatewayEvent applies event the way discordgo does: state first, then
// handlers.
func gatewayEvent(t *testing.T, bot *Bot, event any) {
	t.Helper()
	if err := bot.Session.State.OnInterface(bot.Session, event); err != nil {
		t.Fatal(err)
	}
	bot.live.track(bot.Session, event)
}

func noREST(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(request *http.Request) (*http.Response, error) {
		t.Errorf("unexpected REST call %s %s", request.Method, request.URL.Path)
		return textResponse(request, http.StatusInternalServerError, `{}`), nil
	}
}

func TestGuildAuthorizationAnswersFromLiveState(t *testing.T) {
	bot, calls := liveBot(t, noREST(t))
	snapshot, err := bot.GuildAuthorization(context.Background(), "guild", "mod", "owner")
	if err != nil {
		t.Fatal(err)
	}
	moderate := uint64(discordgo.PermissionModerateMembers)
	if snapshot.Guild.OwnerID != "owner" || !snapshot.Actor.Present || snapshot.Actor.PermissionBits&moderate == 0 ||
		snapshot.Actor.TopRolePosition != 5 || !snapshot.Bot.Present || snapshot.Bot.TopRolePosition != 10 ||
		snapshot.Target == nil || !snapshot.Target.Present {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if calls.Load() != 0 {
		t.Fatalf("REST calls = %d, want 0", calls.Load())
	}
}

func TestGuildAuthorizationFollowsRoleAndMemberEvents(t *testing.T) {
	bot, _ := liveBot(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, http.StatusNotFound, `{"code":10007}`), nil
	})
	moderate := uint64(discordgo.PermissionModerateMembers)
	actor := func() quack.DiscordMemberAuthorization {
		t.Helper()
		snapshot, err := bot.GuildAuthorization(context.Background(), "guild", "mod", "")
		if err != nil {
			t.Fatal(err)
		}
		return snapshot.Actor
	}

	gatewayEvent(t, bot, &discordgo.GuildRoleUpdate{GuildRole: &discordgo.GuildRole{
		GuildID: "guild", Role: &discordgo.Role{ID: "moderator", Position: 5},
	}})
	if got := actor(); !got.Present || got.PermissionBits&moderate != 0 {
		t.Fatalf("role permission removal not seen: %+v", got)
	}

	gatewayEvent(t, bot, &discordgo.GuildRoleUpdate{GuildRole: &discordgo.GuildRole{
		GuildID: "guild", Role: &discordgo.Role{ID: "moderator", Position: 5, Permissions: discordgo.PermissionModerateMembers},
	}})
	gatewayEvent(t, bot, &discordgo.GuildMemberUpdate{Member: &discordgo.Member{
		GuildID: "guild", User: &discordgo.User{ID: "mod"}, JoinedAt: time.Now(),
	}})
	if got := actor(); !got.Present || got.PermissionBits&moderate != 0 || got.TopRolePosition != 0 {
		t.Fatalf("member role removal not seen: %+v", got)
	}

	gatewayEvent(t, bot, &discordgo.GuildMemberRemove{Member: &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "mod"}}})
	if got := actor(); got.Present {
		t.Fatalf("removed member still present: %+v", got)
	}
}

func TestGuildAuthorizationFetchesMissingMembersOnce(t *testing.T) {
	var memberCalls atomic.Int32
	bot, calls := liveBot(t, func(request *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(request.URL.Path, "/guilds/guild/members/late") {
			t.Errorf("unexpected REST call %s", request.URL.Path)
		}
		memberCalls.Add(1)
		return jsonResponse(request, map[string]any{
			"user":      map[string]any{"id": "late", "username": "late"},
			"roles":     []string{"moderator"},
			"joined_at": time.Now().Format(time.RFC3339),
		}), nil
	})
	for range 3 {
		snapshot, err := bot.GuildAuthorization(context.Background(), "guild", "late", "")
		if err != nil || !snapshot.Actor.Present || snapshot.Actor.TopRolePosition != 5 {
			t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
		}
	}
	if memberCalls.Load() != 1 || calls.Load() != 1 {
		t.Fatalf("member fetches = %d, REST calls = %d; want 1 and 1", memberCalls.Load(), calls.Load())
	}

	// A member event drops the fetched copy, so the next check asks again.
	bot.live.track(bot.Session, &discordgo.GuildMemberRemove{Member: &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "late"}}})
	if _, err := bot.GuildAuthorization(context.Background(), "guild", "late", ""); err != nil {
		t.Fatal(err)
	}
	if memberCalls.Load() != 2 {
		t.Fatalf("member fetches after removal = %d, want 2", memberCalls.Load())
	}
}

func TestGuildAuthorizationUsesRESTUntilLive(t *testing.T) {
	var paths []string
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		paths = append(paths, request.URL.Path)
		if strings.HasSuffix(request.URL.Path, "/guilds/guild") {
			return jsonResponse(request, map[string]any{
				"id": "guild", "name": "Guild", "owner_id": "owner",
				"roles": []map[string]any{{"id": "guild", "permissions": "0"}},
			}), nil
		}
		return jsonResponse(request, map[string]any{"user": map[string]any{"id": "mod"}, "roles": []string{}}), nil
	})
	if err := bot.Session.State.GuildAdd(&discordgo.Guild{ID: "guild", OwnerID: "stale", Roles: []*discordgo.Role{{ID: "guild"}}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := bot.GuildAuthorization(context.Background(), "guild", "mod", "")
	if err != nil || snapshot.Guild.OwnerID != "owner" {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
	if len(paths) != 3 {
		t.Fatalf("REST paths = %v, want guild, actor, and bot", paths)
	}

	// After a disconnect, state may be missing events and is not used.
	bot.live.track(bot.Session, &discordgo.Ready{})
	bot.live.track(bot.Session, &discordgo.Disconnect{})
	paths = nil
	if _, err := bot.GuildAuthorization(context.Background(), "guild", "mod", ""); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("REST paths while disconnected = %v", paths)
	}
}

func TestStateMemberWithoutJoinTimeIsNotTrusted(t *testing.T) {
	bot, _ := liveBot(t, func(request *http.Request) (*http.Response, error) {
		return jsonResponse(request, map[string]any{"user": map[string]any{"id": "ghost"}, "roles": []string{"moderator"}}), nil
	})
	// discordgo adds a role-less member like this on a presence update.
	if err := bot.Session.State.MemberAdd(&discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "ghost"}}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := bot.GuildAuthorization(context.Background(), "guild", "ghost", "")
	if err != nil || snapshot.Actor.TopRolePosition != 5 {
		t.Fatalf("snapshot = %+v, err = %v", snapshot, err)
	}
}
