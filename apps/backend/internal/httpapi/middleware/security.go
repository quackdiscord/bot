package middleware

import (
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
)

const csrfHeader = "X-CSRF-Token"

// ValidateSecurityConfig rejects production HTTP settings that would weaken browser boundaries.
func ValidateSecurityConfig(cfg config.Config) error {
	if cfg.API.MaxBodyBytes <= 0 {
		return fmt.Errorf("api.max_body_bytes must be positive")
	}
	if cfg.API.ReadHeaderTimeout <= 0 || cfg.API.ReadTimeout <= 0 || cfg.API.WriteTimeout <= 0 || cfg.API.IdleTimeout <= 0 {
		return fmt.Errorf("all API timeout settings must be positive")
	}
	policies := []config.Limit{
		cfg.Limits.OAuth, cfg.Limits.MemberRead, cfg.Limits.TemplateWrite,
		cfg.Limits.CaseCreate, cfg.Limits.Retry, cfg.Limits.Evidence,
	}
	for _, policy := range policies {
		if policy.Max <= 0 || policy.Window <= 0 {
			return fmt.Errorf("all rate-limit maximum and window settings must be positive")
		}
	}
	if cfg.API.IdempotencyTTL <= 0 {
		return fmt.Errorf("api.idempotency_ttl must be positive")
	}
	if strings.TrimSpace(cfg.Auth.SessionCookieName) == "" || strings.TrimSpace(cfg.Auth.CSRFCookieName) == "" {
		return fmt.Errorf("authentication cookie names must be configured")
	}
	if cfg.Auth.SessionCookieName == cfg.Auth.CSRFCookieName {
		return fmt.Errorf("session and CSRF cookie names must differ")
	}
	if cfg.Auth.SessionTTL <= 0 || cfg.Auth.StateTTL <= 0 {
		return fmt.Errorf("authentication session and state TTL settings must be positive")
	}
	if cfg.Environment != "dev" {
		if len(cfg.API.CORSOrigins) == 0 {
			return fmt.Errorf("api.cors_origins is required outside development")
		}
		if !cfg.Auth.CookieSecure {
			return fmt.Errorf("auth.cookie_secure must be true outside development")
		}
	}
	for _, origin := range cfg.API.CORSOrigins {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return fmt.Errorf("invalid CORS origin %q", origin)
		}
		if strings.Contains(origin, "*") {
			return fmt.Errorf("wildcard CORS origins are not supported")
		}
	}
	for _, proxy := range cfg.API.TrustedProxies {
		if net.ParseIP(proxy) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(proxy); err != nil {
			return fmt.Errorf("invalid trusted proxy %q", proxy)
		}
	}
	return nil
}

// SecurityHeaders applies browser hardening headers that are safe for the JSON API.
func SecurityHeaders(c *gin.Context) {
	c.Header("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	c.Header("Cache-Control", "no-store")
	c.Next()
}

// CORS permits credentialed browser access only from explicitly configured exact origins.
func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := append([]string(nil), allowedOrigins...)
	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin != "" && !slices.Contains(allowed, origin) {
			apierror.Write(c, http.StatusForbidden, apierror.CodeOrigin, "request origin is not allowed")
			return
		}
		if origin != "" {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Idempotency-Key, X-CSRF-Token, X-Request-ID, X-Correlation-ID, X-Quack-Ops-Key")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Vary", "Origin")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// BodyLimit bounds request bodies before JSON or form decoders allocate unbounded memory.
func BodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}

// CSRF protects cookie-authenticated writes with an exact-origin double-submit token check.
func CSRF(auth config.Auth, allowedOrigins []string) gin.HandlerFunc {
	allowed := append([]string(nil), allowedOrigins...)
	return func(c *gin.Context) {
		if !isMutatingMethod(c.Request.Method) || hasBearerCredential(c) {
			c.Next()
			return
		}
		if _, err := c.Cookie(auth.SessionCookieName); err != nil {
			c.Next()
			return
		}
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		if origin == "" || !slices.Contains(allowed, origin) {
			apierror.Write(c, http.StatusForbidden, apierror.CodeCSRF, "CSRF validation failed")
			return
		}
		cookieToken, err := c.Cookie(auth.CSRFCookieName)
		headerToken := strings.TrimSpace(c.GetHeader(csrfHeader))
		if err != nil || cookieToken == "" || headerToken == "" || len(cookieToken) != len(headerToken) || subtle.ConstantTimeCompare([]byte(cookieToken), []byte(headerToken)) != 1 {
			apierror.Write(c, http.StatusForbidden, apierror.CodeCSRF, "CSRF validation failed")
			return
		}
		c.Next()
	}
}

// isMutatingMethod reports whether a method may change server-side state.
func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// hasBearerCredential distinguishes explicitly authenticated non-browser adapter writes from cookie writes.
func hasBearerCredential(c *gin.Context) bool {
	parts := strings.Fields(c.GetHeader("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != ""
}
