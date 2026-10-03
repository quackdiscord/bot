// Package readiness exercises the final process composition across Quack's
// storage, service, optional-module, and HTTP boundaries.
package readiness

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/api"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestCleanInstallComposesEveryAcceptedV5Surface rehearses a clean migration
// and proves the accepted core, appeal, audit, and optional-module HTTP
// registrars can coexist in one process without external network access.
func TestCleanInstallComposesEveryAcceptedV5Surface(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:v5-readiness-clean-install?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open clean database: %v", err)
	}
	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	repository := store.New(db, redisClient)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate clean database: %v", err)
	}
	assertContiguousMigrationLedger(t, db)

	cfg := config.Default()
	cfg.Discord.AppID = "123456789012345678"
	services := quack.New(quack.Deps{Store: repository})
	bot, err := discord.New("Bot readiness-test-token")
	if err != nil {
		t.Fatalf("construct offline Discord session: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	modules, err := moduleintegration.New(ctx, repository, bot, services)
	if err != nil {
		t.Fatalf("compose optional modules: %v", err)
	}
	t.Cleanup(modules.Close)

	cfg.API.MetricsToken = "metrics-secret"
	server, err := api.New(cfg, api.Deps{
		Services: services,
		Store:    repository,
		Redis:    redisClient,
		Discord:  offlineDiscord{},
		Modules:  modules,
	})
	if err != nil {
		t.Fatalf("compose HTTP API: %v", err)
	}
	assertRoutes(t, server, []string{
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
		"GET /guilds/guild/modules/tickets/status",
		"GET /guilds/guild/modules/general-logging/settings",
		"GET /guilds/guild/modules/honeypot/settings",
	})
}

type offlineDiscord struct{}

func (offlineDiscord) Status() (bool, string, int64) { return false, "", 0 }

// assertContiguousMigrationLedger verifies that clean installation records an
// ordered prefix with no duplicate or skipped physical migration versions.
func assertContiguousMigrationLedger(t *testing.T, db *gorm.DB) {
	t.Helper()
	var ledger []struct {
		Version uint64
		Name    string
	}
	if err := db.Raw("SELECT version, name FROM quack_schema_migrations ORDER BY version").Scan(&ledger).Error; err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if len(ledger) != 11 {
		t.Fatalf("migration ledger omitted final v5 migrations: %+v", ledger)
	}
	for index, entry := range ledger {
		want := uint64(index + 1)
		if entry.Version != want {
			t.Fatalf("migration ledger is not contiguous at index %d: got %d want %d", index, entry.Version, want)
		}
	}
	for version, name := range map[int]string{9: "appeals_and_member_access_0200", 10: "v4_historical_import_0400", 11: "final_storage_constraints_0410"} {
		if ledger[version-1].Name != name {
			t.Fatalf("migration %d has name %q, want %q", version, ledger[version-1].Name, name)
		}
	}
}

// assertRoutes verifies the composed API serves each product surface: an
// anonymous request reaches the route rather than the 404 for unknown paths.
func assertRoutes(t *testing.T, handler http.Handler, expected []string) {
	t.Helper()
	for _, route := range expected {
		method, path, _ := strings.Cut(route, " ")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code == http.StatusNotFound {
			t.Errorf("final composition is missing route %s", route)
		}
	}
}
