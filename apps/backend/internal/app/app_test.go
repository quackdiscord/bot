package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/contract"
	"github.com/quackdiscord/bot/internal/discord"
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
	// Every module's intents are requested even with no module on, so
	// switching one on with /setup works without a restart.
	want := discordgo.IntentGuilds | discordgo.IntentGuildMembers | discordgo.IntentGuildModeration |
		discordgo.IntentGuildMessages | discordgo.IntentMessageContent
	if got := bot.Session.Identify.Intents; got != want {
		t.Errorf("intents = %d with no modules on, want %d", got, want)
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

// TestContractCoversEveryMountedRoute checks the HTTP contract against the
// server build really wires: every mounted route, core and module, is
// documented with the same method and path, and every documented operation
// is mounted.
func TestContractCoversEveryMountedRoute(t *testing.T) {
	st := testutil.NewSQLiteRedisStore(t)
	bot, err := discord.New("Bot offline")
	if err != nil {
		t.Fatal(err)
	}
	a, err := build(context.Background(), config.Default(), st, st.Redis(), bot)
	if err != nil {
		t.Fatal(err)
	}
	mounted := map[string]bool{}
	for _, route := range a.server.Routes() {
		mounted[route.Method+" "+route.Path] = true
	}

	routes, err := Routes()
	if err != nil {
		t.Fatal(err)
	}
	spec, err := contract.Build(routes)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for path, item := range document.Paths {
		for method := range item {
			documented[strings.ToUpper(method)+" "+path] = true
		}
	}

	for route := range mounted {
		if !documented[route] {
			t.Errorf("%s is mounted but missing from the contract", route)
		}
	}
	for route := range documented {
		if !mounted[route] {
			t.Errorf("%s is in the contract but not mounted", route)
		}
	}
	if len(mounted) == 0 {
		t.Fatal("no routes mounted")
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

// TestStopListSharesOneDeadline checks that every stop func sees the same
// deadline, so shutdown cannot run longer than api.shutdown_timeout in total.
func TestStopListSharesOneDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	var order []string
	var stops stopList
	for _, name := range []string{"first", "second"} {
		stops.add(name, func(ctx context.Context) error {
			got, ok := ctx.Deadline()
			if !ok || !got.Equal(deadline) {
				t.Errorf("%s deadline = %v, %t; want %v", name, got, ok, deadline)
			}
			order = append(order, name)
			return nil
		})
	}
	if err := stops.run(deadline); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "second,first" {
		t.Errorf("stop order = %v, want newest first", order)
	}
}
