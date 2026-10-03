package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestTemplateReplayRequiresLiveSessionAndCurrentManager(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.BootstrapGuild(context.Background(), quack.BootstrapGuildParams{Starter: quack.StarterTemplate(), DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner"}); err != nil {
		t.Fatal(err)
	}
	discord := routeFakeDiscordClient{userGuilds: []quack.DiscordUserGuild{{ID: "guild-1", Permissions: uint64(discordgo.PermissionManageGuild)}}, botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner"}}
	services := testDeps(store, discord, nil)
	session := routeTestSession("manager")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(middleware.RequestContext, middleware.ErrorEnvelope, httpplatform.EndpointPolicy(httpplatform.FromRepository(store), services.Config))
	SetupRoutes(router, services)
	send := func() *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/templates", strings.NewReader(templateRoutePayload("private-policy")))
		request.Header.Set("Authorization", "Bearer "+session.ID)
		request.Header.Set("Idempotency-Key", "create-policy")
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	first := send()
	if first.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", first.Code, first.Body.String())
	}
	replay := send()
	if replay.Code != first.Code || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay: %d %s", replay.Code, replay.Body.String())
	}
	discord.userGuilds[0].Permissions = 0
	denied := send()
	if denied.Code != http.StatusForbidden || strings.Contains(denied.Body.String(), "private-policy") {
		t.Fatalf("demoted manager replay: %d %s", denied.Code, denied.Body.String())
	}
	discord.userGuilds[0].Permissions = uint64(discordgo.PermissionManageGuild)
	if err := store.DeleteSession(context.Background(), session.ID); err != nil {
		t.Fatal(err)
	}
	revoked := send()
	if revoked.Code != http.StatusUnauthorized || strings.Contains(revoked.Body.String(), "private-policy") {
		t.Fatalf("revoked session replay: %d %s", revoked.Code, revoked.Body.String())
	}
}
