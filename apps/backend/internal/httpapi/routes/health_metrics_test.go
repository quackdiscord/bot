package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

type readyScheduler struct{}

func (readyScheduler) Submit(context.Context, string) bool { return true }
func (readyScheduler) Stats() quack.QueueStats             { return quack.QueueStats{Active: true, Workers: 2} }

type readyDiscord struct{ connected bool }

func (d readyDiscord) Status() (bool, string, int64) { return d.connected, "quack", 1 }

func TestLivenessDoesNotDependOnExternalServices(t *testing.T) {
	router := gin.New()
	SetupRoutes(router, testDeps(nil, nil, nil))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected liveness during dependency outage, got %d", response.Code)
	}
}

func TestReadinessCoversDependenciesQueueMigrationsAndActions(t *testing.T) {
	testutil.SetTestConfig(t)
	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	services := testDeps(store, nil, readyScheduler{})
	router := gin.New()
	SetupRoutes(router, services, readyDiscord{connected: true})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected ready response, got %d body=%s", response.Code, response.Body.String())
	}
	var body readinessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	for _, name := range []string{"database", "redis", "discord", "queue", "migration", "action_capabilities"} {
		if !body.Checks[name].Ready {
			t.Errorf("expected %s readiness: %+v", name, body.Checks[name])
		}
	}
}

func TestMetricsRequireTokenAndExposeOnlyAggregateNames(t *testing.T) {
	testutil.SetTestConfig(t)
	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	cfg := config.Default()
	cfg.API.MetricsToken = "metrics-secret"
	services := testDeps(store, nil, readyScheduler{})
	services.Config = cfg
	router := gin.New()
	SetupRoutes(router, services)
	denied := httptest.NewRecorder()
	router.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("expected metrics denial, got %d", denied.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("X-Quack-Metrics-Key", "metrics-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "quack_cases_total") || strings.Contains(response.Body.String(), "guild_id") {
		t.Fatalf("unexpected metrics response: status=%d body=%s", response.Code, response.Body.String())
	}
}
