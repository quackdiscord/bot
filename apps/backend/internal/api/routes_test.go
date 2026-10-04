package api

import (
	"net/http"
	"testing"

	"github.com/quackdiscord/bot/internal/config"
)

func TestGuildAndMemberRoutesRequireAuthentication(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	routes := []struct{ method, path string }{
		{http.MethodGet, "/guilds"},
		{http.MethodGet, "/guilds/guild/me"},
		{http.MethodPost, "/guilds/guild/cases"},
		{http.MethodPost, "/guilds/guild/templates/template/restore"},
		{http.MethodGet, "/guilds/guild/templates/template/export"},
		{http.MethodPost, "/guilds/guild/templates/import"},
		{http.MethodPost, "/guilds/guild/cases/1/void"},
		{http.MethodGet, "/guilds/guild/action-failures"},
		{http.MethodPost, "/guilds/guild/action-failures/execution/retry"},
		{http.MethodPost, "/guilds/guild/action-failures/execution/dismiss"},
		{http.MethodPost, "/guilds/guild/cases/1/reversals"},
		{http.MethodGet, "/guilds/guild/statistics"},
		{http.MethodGet, "/guilds/guild/appeals"},
		{http.MethodGet, "/guilds/guild/ops/status"},
		{http.MethodGet, "/members/me/guilds/guild/cases"},
		{http.MethodGet, "/members/me/cases/case"},
		{http.MethodPost, "/members/me/cases/case/appeal"},
		{http.MethodGet, "/auth/me"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := send(t, server, route.method, route.path, "", "")
			expectStatus(t, response, http.StatusUnauthorized)
		})
	}
}

func TestUnknownRoutesAndMethodsAreNotFound(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/nope"},
		{http.MethodDelete, "/livez"},
		{http.MethodPut, "/guilds/guild/cases"},
	} {
		response := send(t, server, route.method, route.path, "", "")
		assertEnvelope(t, response, http.StatusNotFound, codeNotFound)
	}
}
