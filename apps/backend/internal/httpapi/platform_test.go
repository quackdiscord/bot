package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
)

func TestPlatformRegistrarRejectsUnsafeProductionConfig(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*config.Config)
	}{
		{name: "missing origins", mutate: func(cfg *config.Config) { cfg.API.CORSOrigins = nil }},
		{name: "wildcard origin", mutate: func(cfg *config.Config) { cfg.API.CORSOrigins = []string{"https://*.example.com"} }},
		{name: "insecure cookie", mutate: func(cfg *config.Config) { cfg.Auth.CookieSecure = false }},
		{name: "unbounded body", mutate: func(cfg *config.Config) { cfg.API.MaxBodyBytes = 0 }},
		{name: "unbounded timeout", mutate: func(cfg *config.Config) { cfg.API.ReadTimeout = 0 }},
		{name: "invalid trusted proxy", mutate: func(cfg *config.Config) { cfg.API.TrustedProxies = []string{"not-a-proxy"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Environment = "production"
			cfg.Auth.CookieSecure = true
			cfg.API.CORSOrigins = []string{"https://dashboard.example.com"}
			test.mutate(&cfg)
			if _, err := NewPlatformRegistrar(cfg); err == nil {
				t.Fatal("expected unsafe production configuration to fail closed")
			}
		})
	}
}

func TestHTTPServerPhasesUseConfiguredBounds(t *testing.T) {
	cfg := config.Default()
	cfg.API.Port = "9090"
	cfg.API.ReadHeaderTimeout = 2 * time.Second
	cfg.API.ReadTimeout = 3 * time.Second
	cfg.API.WriteTimeout = 4 * time.Second
	cfg.API.IdleTimeout = 5 * time.Second
	server := newHTTPServer(cfg, http.NotFoundHandler())
	if server.Addr != ":9090" || server.ReadHeaderTimeout != 2*time.Second || server.ReadTimeout != 3*time.Second || server.WriteTimeout != 4*time.Second || server.IdleTimeout != 5*time.Second {
		t.Fatalf("unexpected server bounds: %+v", server)
	}
}

func TestPlatformSecurityErrorAndCSRFContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	cfg.API.CORSOrigins = []string{"https://dashboard.example.com"}
	cfg.API.MaxBodyBytes = 8
	registrar, err := NewPlatformRegistrar(cfg)
	if err != nil {
		t.Fatalf("new registrar: %v", err)
	}
	router := gin.New()
	if err := registrar.Register(router); err != nil {
		t.Fatalf("register platform: %v", err)
	}
	router.GET("/ok", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	router.POST("/write", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) })
	router.POST("/body", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusNoContent)
	})
	for path, status := range map[string]int{
		"/validation": http.StatusBadRequest,
		"/auth":       http.StatusUnauthorized,
		"/forbidden":  http.StatusForbidden,
		"/conflict":   http.StatusConflict,
		"/dependency": http.StatusServiceUnavailable,
	} {
		status := status
		router.GET(path, func(c *gin.Context) { c.JSON(status, gin.H{"error": "raw secret token=never-return-this"}) })
	}

	request := httptest.NewRequest(http.MethodGet, "/ok", nil)
	request.Header.Set("Origin", "https://evil.example.com")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertStructuredError(t, response, http.StatusForbidden, apierror.CodeOrigin)
	if strings.Contains(response.Body.String(), "evil.example.com") {
		t.Fatalf("origin was reflected in error body: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/ok", nil)
	request.Header.Set("Origin", "https://dashboard.example.com")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "https://dashboard.example.com" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("missing successful security contract: status=%d headers=%v", response.Code, response.Header())
	}

	request = httptest.NewRequest(http.MethodOptions, "/write", nil)
	request.Header.Set("Origin", "https://dashboard.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPut)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !strings.Contains(response.Header().Get("Access-Control-Allow-Methods"), http.MethodPut) {
		t.Fatalf("PUT preflight was not permitted: status=%d headers=%v", response.Code, response.Header())
	}

	request = httptest.NewRequest(http.MethodPost, "/write", nil)
	request.Header.Set("Origin", "https://dashboard.example.com")
	request.AddCookie(&http.Cookie{Name: cfg.Auth.SessionCookieName, Value: "session-secret"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertStructuredError(t, response, http.StatusForbidden, apierror.CodeCSRF)

	request = httptest.NewRequest(http.MethodPost, "/write", nil)
	request.Header.Set("Origin", "https://dashboard.example.com")
	request.Header.Set("X-CSRF-Token", "csrf-token")
	request.AddCookie(&http.Cookie{Name: cfg.Auth.SessionCookieName, Value: "session-secret"})
	request.AddCookie(&http.Cookie{Name: cfg.Auth.CSRFCookieName, Value: "csrf-token"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected valid CSRF request, got %d body=%s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/write", nil)
	request.Header.Set("Authorization", "Bearer adapter-credential")
	request.AddCookie(&http.Cookie{Name: cfg.Auth.SessionCookieName, Value: "session-secret"})
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected explicit bearer adapter to bypass browser CSRF, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/body", bytes.NewBufferString("123456789"))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertStructuredError(t, response, http.StatusRequestEntityTooLarge, apierror.CodeBodyTooLarge)

	for path, expectedCode := range map[string]apierror.Code{
		"/validation": apierror.CodeValidation,
		"/auth":       apierror.CodeAuthentication,
		"/forbidden":  apierror.CodeAuthorization,
		"/conflict":   apierror.CodeConflict,
		"/dependency": apierror.CodeDependency,
	} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("X-Request-ID", "request-test")
		request.Header.Set("X-Correlation-ID", "correlation-test")
		response = httptest.NewRecorder()
		router.ServeHTTP(response, request)
		assertStructuredError(t, response, response.Code, expectedCode)
		if strings.Contains(response.Body.String(), "never-return-this") {
			t.Fatalf("unsafe legacy error escaped for %s: %s", path, response.Body.String())
		}
	}
}

func TestLoggerRedactsQueryCredentialsAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.Default()
	registrar, err := NewPlatformRegistrar(cfg)
	if err != nil {
		t.Fatalf("new registrar: %v", err)
	}
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	router := gin.New()
	if err := registrar.Register(router); err != nil {
		t.Fatalf("register platform: %v", err)
	}
	router.GET("/callback", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/callback?code=oauth-secret&state=state-secret", nil)
	request.Header.Set("Authorization", "Bearer session-secret")
	request.Header.Set("Cookie", "quack_session=cookie-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	logged := output.String()
	for _, secret := range []string{"oauth-secret", "state-secret", "session-secret", "cookie-secret"} {
		if strings.Contains(logged, secret) {
			t.Fatalf("logger exposed %q in %s", secret, logged)
		}
	}
}

func TestPlatformDisablesForwardedClientIPsUnlessProxyIsTrusted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		forwardedFor   string
		wantClientIP   string
	}{
		{name: "untrusted direct client cannot spoof", remoteAddr: "192.0.2.10:1234", forwardedFor: "198.51.100.99", wantClientIP: "192.0.2.10"},
		{name: "explicit proxy forwards client", trustedProxies: []string{"127.0.0.1/32"}, remoteAddr: "127.0.0.1:1234", forwardedFor: "198.51.100.99", wantClientIP: "198.51.100.99"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.API.TrustedProxies = test.trustedProxies
			registrar, err := NewPlatformRegistrar(cfg)
			if err != nil {
				t.Fatalf("new registrar: %v", err)
			}
			router := gin.New()
			if err := registrar.Register(router); err != nil {
				t.Fatalf("register platform: %v", err)
			}
			router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			request := httptest.NewRequest(http.MethodGet, "/ip", nil)
			request.RemoteAddr = test.remoteAddr
			request.Header.Set("X-Forwarded-For", test.forwardedFor)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Body.String() != test.wantClientIP {
				t.Fatalf("expected client IP %q, got status=%d body=%q", test.wantClientIP, response.Code, response.Body.String())
			}
		})
	}
}

// assertStructuredError verifies the stable body and trace identifiers.
func assertStructuredError(t *testing.T, response *httptest.ResponseRecorder, status int, code apierror.Code) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("expected status %d, got %d body=%s", status, response.Code, response.Body.String())
	}
	var body apierror.Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode structured error: %v body=%s", err, response.Body.String())
	}
	if body.Error.Code != code || body.Error.Message == "" || body.Error.RequestID == "" || body.Error.CorrelationID == "" {
		t.Fatalf("unexpected structured error: %+v", body.Error)
	}
}

func TestRecoveryRedactsRequestAndPanicContents(t *testing.T) {
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	registrar, err := NewPlatformRegistrar(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	if err := registrar.Register(router); err != nil {
		t.Fatal(err)
	}
	router.GET("/panic", func(c *gin.Context) { panic("private moderation content") })
	request := httptest.NewRequest(http.MethodGet, "/panic?token=query-secret", nil)
	request.Header.Set("Cookie", "session=cookie-secret")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status: %d", response.Code)
	}
	for _, secret := range []string{"private moderation content", "query-secret", "cookie-secret"} {
		if strings.Contains(output.String(), secret) || strings.Contains(response.Body.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	if !strings.Contains(output.String(), "HTTP request completed") {
		t.Fatal("panic request was not logged")
	}
}
