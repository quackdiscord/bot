package api

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/config"
)

// pipelineServer is a Server with extra test routes behind the global
// pipeline.
func pipelineServer(t *testing.T, cfg config.Config, routes map[string]http.HandlerFunc) *Server {
	t.Helper()
	server := newTestServer(t, cfg, Deps{})
	for pattern, h := range routes {
		server.mux.HandleFunc(pattern, h)
	}
	return server
}

func TestPipelineSecurityContract(t *testing.T) {
	cfg := config.Default()
	cfg.API.CORSOrigins = []string{"https://dashboard.example.com"}
	cfg.API.MaxBodyBytes = 8
	ok := func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, http.StatusOK, map[string]any{"ok": true}) }
	routes := map[string]http.HandlerFunc{
		"GET /ok":     ok,
		"POST /write": ok,
		"POST /body": func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err != nil {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	}
	legacy := map[string]errorCode{
		"/validation": codeValidation,
		"/auth":       codeAuthentication,
		"/forbidden":  codeAuthorization,
		"/conflict":   codeConflict,
		"/dependency": codeDependency,
	}
	statuses := map[string]int{
		"/validation": http.StatusBadRequest,
		"/auth":       http.StatusUnauthorized,
		"/forbidden":  http.StatusForbidden,
		"/conflict":   http.StatusConflict,
		"/dependency": http.StatusServiceUnavailable,
	}
	for path, status := range statuses {
		routes["GET "+path] = func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, status, map[string]any{"error": "raw secret token=never-return-this"})
		}
	}
	server := pipelineServer(t, cfg, routes)

	request := func(method, path, body string, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		r := httptest.NewRequest(method, path, reader)
		for name, value := range headers {
			r.Header.Set(name, value)
		}
		for _, cookie := range cookies {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	session := &http.Cookie{Name: cfg.Auth.SessionCookieName, Value: "session-secret"}
	dashboard := "https://dashboard.example.com"

	t.Run("foreign origin", func(t *testing.T) {
		response := request(http.MethodGet, "/ok", "", map[string]string{"Origin": "https://evil.example.com"})
		assertEnvelope(t, response, http.StatusForbidden, codeOrigin)
		if strings.Contains(response.Body.String(), "evil.example.com") {
			t.Fatalf("origin reflected: %s", response.Body.String())
		}
	})
	t.Run("allowed origin gets CORS and security headers", func(t *testing.T) {
		response := request(http.MethodGet, "/ok", "", map[string]string{"Origin": dashboard})
		h := response.Header()
		if response.Code != http.StatusOK || h.Get("Access-Control-Allow-Origin") != dashboard ||
			h.Get("Access-Control-Allow-Credentials") != "true" || h.Get("X-Content-Type-Options") != "nosniff" ||
			h.Get("Content-Security-Policy") == "" || h.Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d headers=%v", response.Code, h)
		}
	})
	t.Run("preflight", func(t *testing.T) {
		response := request(http.MethodOptions, "/write", "", map[string]string{
			"Origin": dashboard, "Access-Control-Request-Method": http.MethodPut,
		})
		if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), http.MethodPut) {
			t.Fatalf("status=%d headers=%v", response.Code, response.Header())
		}
	})
	t.Run("cookie write without CSRF token", func(t *testing.T) {
		response := request(http.MethodPost, "/write", "", map[string]string{"Origin": dashboard}, session)
		assertEnvelope(t, response, http.StatusForbidden, codeCSRF)
	})
	t.Run("cookie write without origin", func(t *testing.T) {
		response := request(http.MethodPost, "/write", "", map[string]string{csrfHeader: "csrf-token"},
			session, &http.Cookie{Name: cfg.Auth.CSRFCookieName, Value: "csrf-token"})
		assertEnvelope(t, response, http.StatusForbidden, codeCSRF)
	})
	t.Run("cookie write with mismatched token", func(t *testing.T) {
		response := request(http.MethodPost, "/write", "", map[string]string{"Origin": dashboard, csrfHeader: "csrf-tokeN"},
			session, &http.Cookie{Name: cfg.Auth.CSRFCookieName, Value: "csrf-token"})
		assertEnvelope(t, response, http.StatusForbidden, codeCSRF)
	})
	t.Run("cookie write with CSRF token", func(t *testing.T) {
		response := request(http.MethodPost, "/write", "", map[string]string{"Origin": dashboard, csrfHeader: "csrf-token"},
			session, &http.Cookie{Name: cfg.Auth.CSRFCookieName, Value: "csrf-token"})
		expectStatus(t, response, http.StatusOK)
	})
	t.Run("bearer write skips CSRF", func(t *testing.T) {
		response := request(http.MethodPost, "/write", "", map[string]string{"Authorization": "Bearer adapter-credential"}, session)
		expectStatus(t, response, http.StatusOK)
	})
	t.Run("body limit", func(t *testing.T) {
		response := request(http.MethodPost, "/body", "123456789", nil)
		assertEnvelope(t, response, http.StatusRequestEntityTooLarge, codeBodyTooLarge)
	})
	for path, code := range legacy {
		t.Run("raw error "+path, func(t *testing.T) {
			response := request(http.MethodGet, path, "", map[string]string{
				requestIDHeader: "request-test", correlationIDHeader: "correlation-test",
			})
			assertEnvelope(t, response, statuses[path], code)
			if strings.Contains(response.Body.String(), "never-return-this") {
				t.Fatalf("raw error escaped: %s", response.Body.String())
			}
			if !strings.Contains(response.Body.String(), `"request_id":"request-test"`) {
				t.Fatalf("envelope lost the request ID: %s", response.Body.String())
			}
		})
	}
}

// captureLogs sends the default logger to a buffer for the test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &output
}

func TestLoggerOmitsQueryAndCredentials(t *testing.T) {
	logs := captureLogs(t)
	server := pipelineServer(t, config.Default(), map[string]http.HandlerFunc{
		"GET /callback": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	})
	request := httptest.NewRequest(http.MethodGet, "/callback?code=oauth-secret&state=state-secret", nil)
	request.Header.Set("Authorization", "Bearer session-secret")
	request.Header.Set("Cookie", "quack_session=cookie-secret")
	server.ServeHTTP(httptest.NewRecorder(), request)
	for _, secret := range []string{"oauth-secret", "state-secret", "session-secret", "cookie-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatalf("log exposed %q: %s", secret, logs.String())
		}
	}
	if !strings.Contains(logs.String(), `"route":"GET /callback"`) {
		t.Fatalf("log is missing the route pattern: %s", logs.String())
	}
}

func TestRecoveryHidesPanicAndRequest(t *testing.T) {
	logs := captureLogs(t)
	server := pipelineServer(t, config.Default(), map[string]http.HandlerFunc{
		"GET /panic": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("partial"))
			panic("private moderation content")
		},
	})
	request := httptest.NewRequest(http.MethodGet, "/panic?token=query-secret", nil)
	request.Header.Set("Cookie", "session=cookie-secret")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	assertEnvelope(t, response, http.StatusInternalServerError, codeInternal)
	for _, secret := range []string{"private moderation content", "query-secret", "cookie-secret", "partial"} {
		if strings.Contains(logs.String(), secret) || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("leaked %q", secret)
		}
	}
	if !strings.Contains(logs.String(), "HTTP request completed") {
		t.Fatal("panicking request was not logged")
	}
}
