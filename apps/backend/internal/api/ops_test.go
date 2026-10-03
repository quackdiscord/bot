package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
)

type readyScheduler struct{}

func (readyScheduler) Submit(context.Context, string) bool { return true }
func (readyScheduler) Stats() quack.QueueStats             { return quack.QueueStats{Active: true, Workers: 2} }

func TestLivenessIgnoresDependencies(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	expectStatus(t, send(t, server, http.MethodGet, "/livez", "", ""), http.StatusOK)
}

func TestReadinessCoversEveryDependency(t *testing.T) {
	store := migratedStore(t)
	server := newTestServer(t, config.Default(), Deps{
		Services: quack.New(quack.Deps{Store: store, Scheduler: readyScheduler{}}),
		Store:    store,
		Redis:    store.Redis(),
		Discord:  fakeDiscordStatus{connected: true},
	})
	response := send(t, server, http.MethodGet, "/readyz", "", "")
	expectStatus(t, response, http.StatusOK)
	var body readinessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, name := range []string{"database", "redis", "discord", "queue", "migration", "action_capabilities"} {
		if !body.Checks[name].Ready {
			t.Errorf("%s not ready: %+v", name, body.Checks[name])
		}
	}

	down := storeServer(t, store, nil, readyScheduler{}, config.Default())
	expectStatus(t, send(t, down, http.MethodGet, "/readyz", "", ""), http.StatusServiceUnavailable)
}

func TestMetrics(t *testing.T) {
	store := migratedStore(t)
	t.Run("disabled without a key", func(t *testing.T) {
		server := storeServer(t, store, nil, readyScheduler{}, config.Default())
		assertEnvelope(t, send(t, server, http.MethodGet, "/metrics", "", ""), http.StatusNotFound, codeNotFound)
	})

	cfg := config.Default()
	cfg.API.MetricsToken = "metrics-secret"
	server := storeServer(t, store, nil, readyScheduler{}, cfg)
	assertEnvelope(t, send(t, server, http.MethodGet, "/metrics", "", ""), http.StatusForbidden, codeAuthorization)
	assertEnvelope(t, send(t, server, http.MethodGet, "/metrics", "", "", metricsKeyHeader, "metrics-wrong"),
		http.StatusForbidden, codeAuthorization)

	response := send(t, server, http.MethodGet, "/metrics", "", "", metricsKeyHeader, "metrics-secret")
	expectStatus(t, response, http.StatusOK)
	if !strings.Contains(response.Body.String(), "quack_cases_total") || strings.Contains(response.Body.String(), "guild_id") {
		t.Fatalf("metrics = %s", response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}

func TestGlobalOpsStatus(t *testing.T) {
	store := migratedStore(t)
	disabled := storeServer(t, store, nil, nil, config.Default())
	assertEnvelope(t, send(t, disabled, http.MethodGet, "/ops/status", "", ""), http.StatusNotFound, codeNotFound)

	cfg := config.Default()
	cfg.API.OpsToken = "secret"
	server := storeServer(t, store, nil, nil, cfg)
	assertEnvelope(t, send(t, server, http.MethodGet, "/ops/status", "", ""), http.StatusForbidden, codeAuthorization)

	response := send(t, server, http.MethodGet, "/ops/status", "", "", opsKeyHeader, "secret")
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Scope   string `json:"scope"`
		Actions struct {
			Capabilities []struct {
				Executable bool   `json:"executable"`
				Status     string `json:"status"`
			} `json:"capabilities"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Scope != "global" || len(body.Actions.Capabilities) != 5 {
		t.Fatalf("ops = %+v", body)
	}
	for i, capability := range body.Actions.Capabilities {
		want := "implemented"
		if i >= 3 {
			want = "staff_confirmed_reversal"
		}
		if !capability.Executable || capability.Status != want {
			t.Errorf("capability %d = %+v, want executable %s", i, capability, want)
		}
	}
}

func TestGuildOpsStatus(t *testing.T) {
	t.Run("moderator is denied", func(t *testing.T) {
		server, sessionID, _ := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/ops/status", "", sessionID),
			http.StatusForbidden, codeAuthorization)
	})
	t.Run("administrator is allowed", func(t *testing.T) {
		server, sessionID, _ := caseHarness(t, uint64(discordgo.PermissionAdministrator))
		expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/ops/status", "", sessionID), http.StatusOK)
	})
	t.Run("session must be live", func(t *testing.T) {
		store := migratedStore(t)
		server := storeServer(t, store, staffGuilds(uint64(discordgo.PermissionAdministrator)), nil, config.Default())
		session := testSession("user-1")
		session.TokenExpiresAt = session.CreatedAt.Add(-1)
		sessionID := saveSession(t, store, session)
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/ops/status", "", sessionID),
			http.StatusUnauthorized, codeReauthenticate)
	})
	t.Run("ops key", func(t *testing.T) {
		store := migratedStore(t)
		if _, err := store.UpsertGuild(context.Background(), quack.UpsertGuildParams{
			DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
		}); err != nil {
			t.Fatalf("upsert guild: %v", err)
		}
		cfg := config.Default()
		cfg.API.OpsToken = "secret"
		server := storeServer(t, store, nil, nil, cfg)
		expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/ops/status", "", "", opsKeyHeader, "secret"), http.StatusOK)
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/missing/ops/status", "", "", opsKeyHeader, "secret"),
			http.StatusNotFound, codeNotFound)
		assertEnvelope(t, send(t, server, http.MethodGet, "/guilds/guild-1/ops/status", "", "", opsKeyHeader, "wrong"),
			http.StatusUnauthorized, codeAuthentication)
	})
}

func TestStatusReportsDisconnectedDependencies(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	response := send(t, server, http.MethodGet, "/status", "", "", requestIDHeader, "req-test-1")
	expectStatus(t, response, http.StatusOK)
	if got := response.Header().Get(requestIDHeader); got != "req-test-1" {
		t.Fatalf("X-Request-ID = %q, want the caller's ID echoed", got)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, dependency := range []string{"discord", "redis", "database"} {
		if body[dependency]["connected"] != false {
			t.Errorf("%s connected = %v, want false", dependency, body[dependency]["connected"])
		}
	}
}
