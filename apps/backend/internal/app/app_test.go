package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/testutil"
)

// TestBuildWiresEverySurface builds the whole app over a fresh SQLite store
// and an offline bot, and checks that the core, appeal, audit, and module
// routes are all mounted behind authentication.
func TestBuildWiresEverySurface(t *testing.T) {
	st := testutil.NewSQLiteRedisStore(t)
	var ledger []struct {
		Version uint64
		Name    string
	}
	if err := st.DB().Raw("SELECT version, name FROM quack_schema_migrations ORDER BY version").Scan(&ledger).Error; err != nil {
		t.Fatal(err)
	}
	if len(ledger) != 1 || ledger[0].Version != 1 || ledger[0].Name != "baseline" {
		t.Fatalf("migration ledger = %+v, want only the baseline", ledger)
	}

	bot, err := discord.New("Bot offline")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.API.MetricsToken = "metrics-secret"
	a, err := build(context.Background(), cfg, st, st.Redis(), bot)
	if err != nil {
		t.Fatal(err)
	}
	if bot.Session.Identify.Intents != discordgo.IntentGuilds {
		t.Errorf("intents = %d with no modules on, want only guilds", bot.Session.Identify.Intents)
	}
	for _, route := range []string{
		"GET /livez",
		"GET /readyz",
		"GET /metrics",
		"GET /status",
		"GET /guilds/guild/templates",
		"POST /guilds/guild/cases",
		"POST /guilds/guild/cases/1/void",
		"GET /guilds/guild/audit-log",
		"GET /guilds/guild/statistics",
		"GET /guilds/guild/appeals",
		"POST /guilds/guild/appeals/appeal/accept",
		"GET /members/me/guilds/guild/cases",
		"POST /members/me/cases/case/appeal",
	} {
		if code := serve(a, route); code == http.StatusNotFound {
			t.Errorf("%s is not mounted", route)
		}
	}
	for _, route := range []string{
		"GET /guilds/guild/modules/tickets/status",
		"GET /guilds/guild/modules/tickets/ticket/transcript",
		"POST /guilds/guild/modules/tickets/ticket/cancel",
		"GET /guilds/guild/modules/general-logging/settings",
		"PUT /guilds/guild/modules/general-logging/settings",
		"POST /guilds/guild/modules/general-logging/repair-channel/channel",
		"GET /guilds/guild/modules/honeypot/settings",
		"PUT /guilds/guild/modules/honeypot/settings",
		"POST /guilds/guild/modules/honeypot/repair",
	} {
		if code := serve(a, route); code != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401 from a mounted, authenticated route", route, code)
		}
	}
}

// serve sends route, a "METHOD /path" pair, to the built API and returns
// the status code.
func serve(a *quackApp, route string) int {
	method, path, _ := strings.Cut(route, " ")
	response := httptest.NewRecorder()
	a.server.ServeHTTP(response, httptest.NewRequest(method, path, nil))
	return response.Code
}

// TestIntentsFollowEnabledModules checks that only enabled modules add
// gateway intents, and that the honeypot never asks for message content.
func TestIntentsFollowEnabledModules(t *testing.T) {
	ctx := context.Background()
	st := testutil.NewSQLiteRedisStore(t)
	registry := modules.NewRegistry(st.DB())
	intents := func() discordgo.Intent {
		t.Helper()
		bot, err := discord.New("Bot offline")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := build(ctx, config.Default(), st, st.Redis(), bot); err != nil {
			t.Fatal(err)
		}
		return bot.Session.Identify.Intents
	}
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild", ModuleID: modules.Honeypots, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got := intents(); got&discordgo.IntentGuildMessages == 0 || got&discordgo.IntentMessageContent != 0 {
		t.Fatalf("honeypot intents = %d", got)
	}
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild", ModuleID: modules.GeneralLogging, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	want := discordgo.IntentGuildMembers | discordgo.IntentGuildModeration | discordgo.IntentMessageContent
	if got := intents(); got&want != want {
		t.Fatalf("logging intents = %d", got)
	}
}
