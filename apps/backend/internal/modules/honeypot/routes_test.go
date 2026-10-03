package honeypot_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
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

func routeEngine(fixture *fixture, canManage bool) http.Handler {
	mux := testMux{http.NewServeMux()}
	honeypot.RegisterRoutes(mux, fixture.service, func(r *http.Request) (modules.Actor, error) {
		if r.Header.Get("Authorization") == "" {
			return modules.Actor{}, errors.New("missing session")
		}
		return modules.Actor{GuildID: r.PathValue("guildID"), DiscordUserID: "admin", CanManage: canManage}, nil
	})
	return mux
}

func request(t *testing.T, engine http.Handler, method, path string, body any, authenticated bool) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if authenticated {
		req.Header.Set("Authorization", "session")
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	return response
}

func TestRoutesSettingsStatusRepairAndAuthorization(t *testing.T) {
	fixture := setup(t)
	engine := routeEngine(fixture, true)
	path := "/guilds/guild-a/modules/honeypot/settings"
	response := request(t, engine, http.MethodPut, path, map[string]any{"enabled": true, "settings": map[string]any{"channel_discord_id": "trap", "template_id": "template", "exempt_role_discord_ids": []string{"trusted"}}}, true)
	if response.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, engine, http.MethodGet, path, nil, true)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	if err := fixture.service.HandleDeletedChannel(t.Context(), "guild-a", "trap"); err != nil {
		t.Fatal(err)
	}
	response = request(t, engine, http.MethodGet, "/guilds/guild-a/modules/honeypot/status", nil, true)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte("configured honeypot channel was deleted")) {
		t.Fatalf("drift status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, engine, http.MethodPost, "/guilds/guild-a/modules/honeypot/repair", nil, true)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"enabled":true`)) {
		t.Fatalf("repair status=%d body=%s", response.Code, response.Body.String())
	}
	response = request(t, engine, http.MethodGet, path, nil, false)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status=%d", response.Code)
	}
	response = request(t, routeEngine(fixture, false), http.MethodGet, path, nil, true)
	if response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRoutesRejectUnsafeConfiguration(t *testing.T) {
	fixture := setup(t)
	engine := routeEngine(fixture, true)
	path := "/guilds/guild-a/modules/honeypot/settings"
	response := request(t, engine, http.MethodPut, path, map[string]any{"enabled": true, "settings": map[string]any{"channel_discord_id": "trap"}}, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid settings status=%d body=%s", response.Code, response.Body.String())
	}
	fixture.validator.templateErr = errors.New("archived")
	response = request(t, engine, http.MethodPut, path, map[string]any{"enabled": true, "settings": map[string]any{"channel_discord_id": "trap", "template_id": "template"}}, true)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("archived template status=%d body=%s", response.Code, response.Body.String())
	}
}
