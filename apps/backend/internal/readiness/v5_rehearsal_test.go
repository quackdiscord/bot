// Package readiness exercises the final process composition across Quack's
// storage, service, optional-module, and HTTP boundaries.
package readiness

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/routes"
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
	session, err := discordgo.New("Bot readiness-test-token")
	if err != nil {
		t.Fatalf("construct offline Discord session: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	modules, err := moduleintegration.New(ctx, repository, session, services)
	if err != nil {
		t.Fatalf("compose optional modules: %v", err)
	}
	t.Cleanup(modules.Close)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if err := routes.SetupRoutesWithModules(engine, &routes.Deps{Services: services, Config: cfg, Store: repository}, modules); err != nil {
		t.Fatalf("compose HTTP routes: %v", err)
	}
	assertRoutes(t, engine, []string{
		"GET /livez",
		"GET /readyz",
		"GET /metrics",
		"GET /status",
		"GET /guilds/:discordGuildID/templates",
		"POST /guilds/:discordGuildID/cases",
		"POST /guilds/:discordGuildID/cases/:caseRef/void",
		"GET /guilds/:discordGuildID/audit-log",
		"GET /guilds/:discordGuildID/statistics",
		"GET /guilds/:discordGuildID/appeals",
		"POST /guilds/:discordGuildID/appeals/:appealID/accept",
		"GET /members/me/guilds/:guildID/cases",
		"POST /members/me/cases/:caseID/appeal",
		"GET /guilds/:discordGuildID/modules/tickets/status",
		"GET /guilds/:discordGuildID/modules/general-logging/settings",
		"GET /guilds/:discordGuildID/modules/honeypot/settings",
	})
}

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

// assertRoutes verifies the final router exposes each product surface through
// the central composition point rather than package-local tests alone.
func assertRoutes(t *testing.T, engine *gin.Engine, expected []string) {
	t.Helper()
	present := make(map[string]bool, len(engine.Routes()))
	for _, route := range engine.Routes() {
		present[fmt.Sprintf("%s %s", route.Method, route.Path)] = true
	}
	for _, route := range expected {
		if !present[route] {
			t.Errorf("final composition is missing route %s", route)
		}
	}
}
