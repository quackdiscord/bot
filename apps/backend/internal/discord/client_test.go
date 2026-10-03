package discord

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestMemberAuthorizationCalculatesPermissionsAndHierarchy(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Position: 0, Permissions: discordgo.PermissionViewChannel},
			{ID: "moderator", Position: 10, Permissions: discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers},
			{ID: "administrator", Position: 5, Permissions: discordgo.PermissionAdministrator},
		},
	}
	moderator := memberAuthorization(guild, &discordgo.Member{
		User:  &discordgo.User{ID: "mod", Username: "Moderator"},
		Roles: []string{"moderator"},
	})
	want := uint64(discordgo.PermissionViewChannel | discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers)
	if !moderator.Present || moderator.Bot || moderator.TopRolePosition != 10 || moderator.PermissionBits&want != want {
		t.Fatalf("moderator = %+v", moderator)
	}
	admin := memberAuthorization(guild, &discordgo.Member{
		User:  &discordgo.User{ID: "admin", Bot: true},
		Roles: []string{"administrator"},
	})
	if !admin.Bot || admin.PermissionBits&uint64(discordgo.PermissionBanMembers) == 0 {
		t.Fatalf("administrator was not expanded: %+v", admin)
	}
	owner := memberAuthorization(guild, &discordgo.Member{User: &discordgo.User{ID: "owner"}})
	if owner.PermissionBits&uint64(discordgo.PermissionAdministrator) == 0 {
		t.Fatalf("owner was not expanded: %+v", owner)
	}
}

func TestGuildAuthorizationMapsMissingGuild(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusForbidden:           quack.ErrBotNotInGuild,
		http.StatusNotFound:            quack.ErrBotNotInGuild,
		http.StatusInternalServerError: quack.ErrAuthorizationUnavailable,
	} {
		bot := testBot(t, func(request *http.Request) (*http.Response, error) {
			return textResponse(request, status, `{}`), nil
		})
		if _, err := bot.GuildAuthorization(context.Background(), "guild", "actor", ""); !errors.Is(err, want) {
			t.Errorf("status %d: got %v, want %v", status, err, want)
		}
	}
}

func TestEnforcementCarriesContextAndDoesNotRetry(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "trace")
	calls := 0
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context().Value(key{}) != "trace" {
			t.Error("request lost its context")
		}
		return textResponse(request, http.StatusBadGateway, `{"message":"upstream unavailable"}`), nil
	})
	_, err := bot.BanMember(ctx, "guild", "member", 0, "case")
	var classified quack.DiscordError
	if calls != 1 || !errors.As(err, &classified) || !classified.OutcomeUncertain || classified.Retryable {
		t.Fatalf("expected one uncertain attempt: calls=%d error=%+v", calls, err)
	}
}
