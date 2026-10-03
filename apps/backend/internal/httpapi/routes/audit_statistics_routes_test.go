package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAuditStatisticsRegistrarMountsAuthenticatedGuildRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/guilds")
	RegisterAuditStatisticsStaffRoutes(group, testDeps(nil, nil, nil))
	found := false
	for _, route := range router.Routes() {
		if route.Method == http.MethodGet && route.Path == "/guilds/:discordGuildID/statistics" {
			found = true
		}
	}
	if !found {
		t.Fatal("statistics registrar did not expose the QI-2 integration contract")
	}
	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/statistics", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("statistics route bypassed authenticated guild context: status=%d body=%s", response.Code, response.Body.String())
	}
}
