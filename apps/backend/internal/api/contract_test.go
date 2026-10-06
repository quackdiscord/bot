package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
)

// TestRouteTableMatchesMux checks that every recorded route is what the mux
// resolves its path to, so the contract generated from the table describes
// the routes actually served, module routes included.
func TestRouteTableMatchesMux(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{Modules: func(mux *ModuleMux) {
		mux.Handle("GET /example/{itemID}", modules.Doc{ID: "getExample", Summary: "Example"},
			http.NotFoundHandler())
		mux.HandleWrite("PUT /example/{itemID}", modules.Doc{ID: "putExample", Summary: "Example"},
			func(*http.Request) bool { return true }, http.NotFoundHandler())
	}})
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	routes := server.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes recorded")
	}
	for _, route := range routes {
		pattern := route.Method + " " + route.Path
		path := placeholder.ReplaceAllString(route.Path, "x")
		if _, got := server.mux.Handler(httptest.NewRequest(route.Method, path, nil)); got != pattern {
			t.Errorf("%s %s resolves to %q, want %q", route.Method, path, got, pattern)
		}
		if route.Doc.ID == "" || route.Doc.Summary == "" {
			t.Errorf("%s has no Doc ID or Summary", pattern)
		}
		if isWrite(route.Method) && route.Auth == AuthSession && !route.Idempotent &&
			route.Path != "/auth/logout" && route.Path != "/auth/logout-all" {
			t.Errorf("%s is a session write without an Idempotency-Key", pattern)
		}
	}
	last := routes[len(routes)-1]
	if last.Path != modulePrefix+"/example/{itemID}" || !last.Idempotent || !last.Guild {
		t.Errorf("module write recorded as %+v", last)
	}
}
