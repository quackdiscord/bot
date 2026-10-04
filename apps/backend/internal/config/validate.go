package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Validate reports every missing or unsafe setting at once, so an operator
// can fix a deployment in one pass. It is meant for `quack serve`; tools that
// only need the database check database.dsn themselves.
func (c Config) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	blank := func(s string) bool { return strings.TrimSpace(s) == "" }

	if !slices.Contains([]string{"dev", "test", "staging", "production"}, c.Environment) {
		fail("environment must be dev, test, staging, or production, not %q", c.Environment)
	}
	if blank(c.API.Port) {
		fail("api.port is required")
	}
	if c.API.MaxBodyBytes <= 0 {
		fail("api.max_body_bytes must be positive")
	}
	for _, d := range []struct {
		key   string
		value time.Duration
	}{
		{"api.read_header_timeout", c.API.ReadHeaderTimeout},
		{"api.read_timeout", c.API.ReadTimeout},
		{"api.write_timeout", c.API.WriteTimeout},
		{"api.idle_timeout", c.API.IdleTimeout},
		{"api.shutdown_timeout", c.API.ShutdownTimeout},
		{"api.idempotency_ttl", c.API.IdempotencyTTL},
		{"auth.session_ttl", c.Auth.SessionTTL},
		{"auth.state_ttl", c.Auth.StateTTL},
	} {
		if d.value <= 0 {
			fail("%s must be a positive duration", d.key)
		}
	}
	for _, l := range []struct {
		key   string
		value Limit
	}{
		{"limits.oauth", c.Limits.OAuth},
		{"limits.member_read", c.Limits.MemberRead},
		{"limits.template_write", c.Limits.TemplateWrite},
		{"limits.case_create", c.Limits.CaseCreate},
		{"limits.retry", c.Limits.Retry},
		{"limits.evidence", c.Limits.Evidence},
	} {
		if l.value.Max <= 0 || l.value.Window <= 0 {
			fail("%s must have a positive max and window, got %s", l.key, l.value)
		}
	}
	if blank(c.Auth.SessionCookieName) || blank(c.Auth.CSRFCookieName) {
		fail("auth.session_cookie_name and auth.csrf_cookie_name are required")
	} else if c.Auth.SessionCookieName == c.Auth.CSRFCookieName {
		fail("auth.session_cookie_name and auth.csrf_cookie_name must differ")
	}
	for _, origin := range c.API.CORSOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(origin, "*") {
			fail("api.cors_origins: %q is not an exact http(s) origin", origin)
		}
	}
	for _, proxy := range c.API.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				fail("api.trusted_proxies: %q is not an IP or CIDR", proxy)
			}
		}
	}
	if c.Queue.Size <= 0 || c.Queue.Workers <= 0 {
		fail("queue.size and queue.workers must be positive")
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(c.Log.Level)); err != nil {
		fail("log.level must be debug, info, warn, or error, not %q", c.Log.Level)
	}
	if blank(c.Database.DSN) {
		fail("database.dsn is required")
	}
	if blank(c.Redis.URL) {
		fail("redis.url is required")
	}
	if blank(c.Discord.Token) {
		fail("discord.token is required")
	}
	if blank(c.Discord.AppID) {
		fail("discord.app_id is required")
	}

	if c.Environment != "dev" {
		if len(c.API.CORSOrigins) == 0 {
			fail("api.cors_origins is required outside dev")
		}
		if !c.Auth.CookieSecure {
			fail("auth.cookie_secure must be true outside dev")
		}
	}
	if c.Environment == "staging" || c.Environment == "production" {
		if blank(c.Discord.ClientSecret) {
			fail("discord.client_secret is required in %s", c.Environment)
		}
		if blank(c.API.OpsToken) {
			fail("api.ops_token is required in %s", c.Environment)
		}
		if blank(c.API.MetricsToken) {
			fail("api.metrics_token is required in %s", c.Environment)
		}
		redirect, err := url.Parse(c.Discord.OAuthRedirectURI)
		if err != nil || redirect.Scheme != "https" || redirect.Host == "" ||
			redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" {
			fail("discord.oauth_redirect_uri must be a plain https URL in %s", c.Environment)
		}
		scopes := strings.Fields(c.Discord.OAuthScopes)
		if !slices.Contains(scopes, "identify") || !slices.Contains(scopes, "guilds") {
			fail("discord.oauth_scopes must include identify and guilds")
		}
	}
	return errors.Join(errs...)
}
