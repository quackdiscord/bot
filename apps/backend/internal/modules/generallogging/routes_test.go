package generallogging_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	logmodule "github.com/quackdiscord/bot/internal/modules/generallogging"
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
	_, service, _, _ := setup(t)
	for _, test := range []struct {
		name         string
		canManage    bool
		method, path string
		want         int
	}{
		{"status", true, http.MethodGet, "/general-logging/status", http.StatusOK},
		{"settings write needs Manage Guild", false, http.MethodPut, "/general-logging/settings", http.StatusForbidden},
		{"malformed settings", true, http.MethodPut, "/general-logging/settings", http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			mux := testMux{http.NewServeMux()}
			logmodule.RegisterRoutes(mux, service, func(r *http.Request) (logmodule.Actor, error) {
				return logmodule.Actor{GuildID: r.PathValue("guildID"), DiscordUserID: "admin", CanManage: test.canManage}, nil
			})
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(test.method, "/guilds/guild-a/modules"+test.path, strings.NewReader("{")))
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}
