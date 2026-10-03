package tickets_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
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

func TestRoutes(t *testing.T) {
	service, adapter, _, _ := setup(t)
	for _, test := range []struct {
		name         string
		actor        modules.Actor
		method, path string
		want         int
	}{
		{"status", modules.Actor{CanModerate: true}, http.MethodGet, "/tickets/status", http.StatusOK},
		{"settings write needs Manage Guild", modules.Actor{CanModerate: true}, http.MethodPut, "/tickets/settings", http.StatusForbidden},
		{"resolve needs moderation", modules.Actor{CanManage: true}, http.MethodPost, "/tickets/missing/resolve", http.StatusForbidden},
		{"cancel needs an existing ticket", modules.Actor{CanModerate: true}, http.MethodPost, "/tickets/missing/cancel", http.StatusForbidden},
		{"missing ticket", modules.Actor{CanModerate: true}, http.MethodGet, "/tickets/missing", http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			mux := testMux{http.NewServeMux()}
			tickets.RegisterRoutes(mux, service, adapter, func(r *http.Request) (modules.Actor, error) {
				actor := test.actor
				actor.GuildID, actor.DiscordUserID = r.PathValue("guildID"), "staff"
				return actor, nil
			})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(test.method, "/guilds/guild-a/modules"+test.path, nil))
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

// TestHTTPCloseArchivesLikeDiscord checks that resolving or cancelling from
// the dashboard captures the transcript and archives the channel, as the
// Discord close button does.
func TestHTTPCloseArchivesLikeDiscord(t *testing.T) {
	service, adapter, client, _ := setup(t)
	ctx := context.Background()
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	mux := testMux{http.NewServeMux()}
	tickets.RegisterRoutes(mux, service, adapter, func(r *http.Request) (modules.Actor, error) {
		return modules.Actor{GuildID: r.PathValue("guildID"), DiscordUserID: "staff", CanModerate: true}, nil
	})
	for _, action := range []string{"resolve", "cancel"} {
		t.Run(action, func(t *testing.T) {
			ticket, err := adapter.Open(ctx, member)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			path := "/guilds/guild-a/modules/tickets/" + ticket.ID + "/" + action
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`)))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ticket"`) {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if last := client.removed[len(client.removed)-1]; last != ticket.ThreadDiscordChannelID {
				t.Fatalf("archived %q, want %q", last, ticket.ThreadDiscordChannelID)
			}
			transcript, err := service.Transcript(ctx, modules.Actor{GuildID: "guild-a", CanModerate: true}, ticket.ID)
			if err != nil || transcript.Content != "captured" {
				t.Fatalf("transcript = %+v, %v; want the captured one", transcript, err)
			}
		})
	}
}
