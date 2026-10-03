package tickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

// testMux mounts module routes under /guilds/{guildID}/modules and enforces
// write permissions with a 403, standing in for api.ModuleMux.
type testMux struct{ *http.ServeMux }

func (m testMux) Handle(pattern string, h http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	m.ServeMux.Handle(method+" /guilds/{guildID}/modules"+path, h)
}

func (m testMux) HandleWrite(pattern string, allowed func(*http.Request) bool, h http.Handler) {
	m.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed(r) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	}))
}

func routesFor(service *tickets.Service, closer tickets.Closer, actor modules.Actor) testMux {
	mux := testMux{http.NewServeMux()}
	tickets.RegisterRoutes(mux, service, closer, func(r *http.Request) (modules.Actor, error) {
		actor := actor
		actor.GuildID = r.PathValue("guildID")
		return actor, nil
	})
	return mux
}

func TestRoutes(t *testing.T) {
	_, service, _ := setup(t)
	adapter := tickets.NewDiscordAdapter(service, &discordFake{})
	ticket, err := adapter.Open(context.Background(), modules.Actor{GuildID: "guild-a", DiscordUserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	staff := modules.Actor{DiscordUserID: "staff", CanModerate: true}
	for _, test := range []struct {
		name         string
		actor        modules.Actor
		method, path string
		want         int
	}{
		{"status", staff, http.MethodGet, "/tickets/status", http.StatusOK},
		{"settings read needs Manage Guild", staff, http.MethodGet, "/tickets/settings", http.StatusForbidden},
		{"settings write needs Manage Guild", staff, http.MethodPut, "/tickets/settings", http.StatusForbidden},
		{"queue", staff, http.MethodGet, "/tickets/queue?status=open", http.StatusOK},
		{"detail", staff, http.MethodGet, "/tickets/" + ticket.ID, http.StatusOK},
		{"other member's ticket", modules.Actor{DiscordUserID: "other"}, http.MethodGet, "/tickets/" + ticket.ID, http.StatusForbidden},
		{"missing ticket", staff, http.MethodGet, "/tickets/missing", http.StatusNotFound},
		{"open ticket has no transcript", staff, http.MethodGet, "/tickets/" + ticket.ID + "/transcript", http.StatusNotFound},
		{"close needs a visible ticket", modules.Actor{DiscordUserID: "other"}, http.MethodPost, "/tickets/" + ticket.ID + "/close", http.StatusForbidden},
		{"reopen is gone", staff, http.MethodPost, "/tickets/" + ticket.ID + "/reopen", http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, "/guilds/guild-a/modules"+test.path, strings.NewReader(`{}`))
			routesFor(service, adapter, test.actor).ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body = %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

// TestEveryCloseRouteRunsTheDiscordClose checks close and its older names
// resolve, publish, and delete like the Discord button, ignoring any
// transcript in the body.
func TestEveryCloseRouteRunsTheDiscordClose(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	for _, action := range []string{"close", "resolve", "cancel"} {
		t.Run(action, func(t *testing.T) {
			ticket, err := adapter.Open(ctx, member)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			path := "/guilds/guild-a/modules/tickets/" + ticket.ID + "/" + action
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"transcript":"forged"}`))
			routesFor(service, adapter, modules.Actor{DiscordUserID: "member"}).ServeHTTP(response, request)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"resolved"`) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if last := client.deleted[len(client.deleted)-1]; last != ticket.ThreadDiscordChannelID {
				t.Fatalf("deleted %q, want %q", last, ticket.ThreadDiscordChannelID)
			}
			if got := transcriptOf(t, service, member, ticket.ID); strings.Contains(got, "forged") || !strings.Contains(got, "captured") {
				t.Fatalf("transcript = %q", got)
			}
		})
	}
}

func TestComponentsAndSetupInstallBesideCoreRoutes(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	bot, err := discord.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	services := quack.New(quack.Deps{})
	router := discord.NewRouter(bot, services, nil)
	// The router panics on a duplicate or empty route.
	tickets.New(db, modules.NewRegistry(db), nil, nil, bot.Session, services.Guilds).RegisterComponents(router)
}
