package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// writeFile writes a TOML file into a temp dir and returns its path.
func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quack.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	t.Chdir(t.TempDir()) // no quack.toml here
	cfg, err := load("", []string{"PATH=/bin", "QUACK_TEST_MYSQL_DSN=ignored", "QUACK_QUEUE_WORKERS="})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("got %+v, want defaults", cfg)
	}
}

func TestLoadEnvironment(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg, err := load("", []string{
		"QUACK_ENVIRONMENT=production",
		"QUACK_API_PORT=9090",
		"QUACK_API_MAX_BODY_BYTES=4294967296",
		"QUACK_API_CORS_ORIGINS= https://a.example, ,https://b.example ,",
		"QUACK_API_TRUSTED_PROXIES=10.0.0.0/8",
		"QUACK_API_READ_TIMEOUT=2s",
		"QUACK_AUTH_SESSION_TTL=12h",
		"QUACK_AUTH_COOKIE_SECURE=false",
		"QUACK_DISCORD_TOKEN=bot-token",
		"QUACK_DISCORD_APP_ID=123",
		"QUACK_DISCORD_COMMAND_PRUNE=true",
		"QUACK_LIMITS_MEMBER_READ=7/30s",
		"QUACK_DATABASE_DSN=user@tcp(db)/quack",
		"QUACK_REDIS_URL=redis://redis:6379/0",
		"QUACK_QUEUE_WORKERS=7",
		"QUACK_LOG_LEVEL=debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.Environment = "production"
	want.API.Port = "9090"
	want.API.MaxBodyBytes = 4294967296
	want.API.CORSOrigins = []string{"https://a.example", "https://b.example"}
	want.API.TrustedProxies = []string{"10.0.0.0/8"}
	want.API.ReadTimeout = 2 * time.Second
	want.Auth.SessionTTL = 12 * time.Hour
	want.Auth.CookieSecure = false
	want.Discord.Token = "bot-token"
	want.Discord.AppID = "123"
	want.Discord.CommandPrune = true
	want.Limits.MemberRead = Limit{Max: 7, Window: 30 * time.Second}
	want.Database.DSN = "user@tcp(db)/quack"
	want.Redis.URL = "redis://redis:6379/0"
	want.Queue.Workers = 7
	want.Log.Level = "debug"
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestLoadEnvironmentDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, mode := range []string{"dev", "test", "staging", "production"} {
		t.Run(mode, func(t *testing.T) {
			cfg, err := load("", []string{"QUACK_ENVIRONMENT=" + mode})
			if err != nil {
				t.Fatal(err)
			}
			dev := mode == "dev"
			if cfg.Auth.CookieSecure == dev {
				t.Errorf("cookie_secure = %v", cfg.Auth.CookieSecure)
			}
			if (len(cfg.API.CORSOrigins) > 0) != dev {
				t.Errorf("cors_origins = %v", cfg.API.CORSOrigins)
			}
		})
	}
}

func TestLoadFile(t *testing.T) {
	t.Chdir(t.TempDir())
	path := writeFile(t, `
[api]
port = "7000"
shutdown_timeout = "5s"
cors_origins = ["https://dash.example"]

[limits]
oauth = "3/1h"

[discord]
token = "file-token"
`)
	env := []string{"QUACK_API_PORT=7100"}

	for name, args := range map[string]struct {
		path    string
		environ []string
	}{
		"flag":         {path: path, environ: env},
		"QUACK_CONFIG": {environ: append(env, "QUACK_CONFIG="+path)},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := load(args.path, args.environ)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.API.Port != "7100" {
				t.Errorf("port = %q, want the env value to win", cfg.API.Port)
			}
			if cfg.API.ShutdownTimeout != 5*time.Second || cfg.Discord.Token != "file-token" {
				t.Errorf("file values not applied: %+v", cfg)
			}
			if !reflect.DeepEqual(cfg.API.CORSOrigins, []string{"https://dash.example"}) {
				t.Errorf("cors_origins = %v", cfg.API.CORSOrigins)
			}
			if cfg.Limits.OAuth != (Limit{Max: 3, Window: time.Hour}) {
				t.Errorf("limits.oauth = %v", cfg.Limits.OAuth)
			}
			if cfg.Limits.Retry != Default().Limits.Retry {
				t.Errorf("unset limit lost its default: %v", cfg.Limits.Retry)
			}
		})
	}
}

func TestLoadDefaultFile(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(DefaultFile, []byte("[queue]\nsize = 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Queue.Size != 5 {
		t.Fatalf("queue.size = %d, want 5 from %s", cfg.Queue.Size, DefaultFile)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	tests := []struct {
		name    string
		path    string
		environ []string
	}{
		{name: "missing explicit file", path: "nope.toml"},
		{name: "missing QUACK_CONFIG file", environ: []string{"QUACK_CONFIG=nope.toml"}},
		{name: "bad toml", path: writeFile(t, "[api\n")},
		{name: "unknown file key", path: writeFile(t, "[api]\nprot = \"1\"\n")},
		{name: "unknown env key", environ: []string{"QUACK_API_PROT=1"}},
		{name: "bad int", environ: []string{"QUACK_QUEUE_WORKERS=many"}},
		{name: "bad bool", environ: []string{"QUACK_AUTH_COOKIE_SECURE=maybe"}},
		{name: "bad duration", environ: []string{"QUACK_API_READ_TIMEOUT=15"}},
		{name: "bad limit", environ: []string{"QUACK_LIMITS_RETRY=10"}},
		{name: "overflow", environ: []string{"QUACK_API_MAX_BODY_BYTES=9223372036854775808"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := load(test.path, test.environ); err == nil {
				t.Fatal("want error")
			}
		})
	}
}

func TestEnvKey(t *testing.T) {
	tests := map[string]string{
		"QUACK_ENVIRONMENT":           "environment",
		"QUACK_DISCORD_TOKEN":         "discord.token",
		"QUACK_DISCORD_COMMAND_PRUNE": "discord.command_prune",
		"QUACK_DATABASE_DSN":          "database.dsn",
		"QUACK_API_CORS_ORIGINS":      "api.cors_origins",
		"QUACK_LIMITS_TEMPLATE_WRITE": "limits.template_write",
		"QUACK_CONFIG":                "",
		"QUACK_TEST_MYSQL_DSN":        "",
		"QUACK_UNKNOWN_SECTION_THING": "",
	}
	for name, want := range tests {
		if got, _ := envKey(name, "x"); got != want {
			t.Errorf("envKey(%q) = %q, want %q", name, got, want)
		}
	}
}

// validConfig is a dev config that passes Validate.
func validConfig() Config {
	cfg := Default()
	cfg.Database.DSN = "user:password@tcp(database:3306)/quack"
	cfg.Redis.URL = "redis://redis:6379/0"
	cfg.Discord.Token = "bot-token"
	cfg.Discord.AppID = "application-id"
	return cfg
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   []string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{
			name:   "environment",
			mutate: func(c *Config) { c.Environment = "prod-ish" },
			want:   []string{"environment"},
		},
		{
			name: "every missing value is reported",
			mutate: func(c *Config) {
				c.Database.DSN = ""
				c.Redis.URL = ""
				c.Discord.Token = ""
				c.Queue.Workers = 0
				c.API.ShutdownTimeout = 0
				c.Limits.Evidence.Window = -1
				c.Log.Level = "loud"
			},
			want: []string{
				"database.dsn", "redis.url", "discord.token", "queue.workers",
				"api.shutdown_timeout", "limits.evidence", "log.level",
			},
		},
		{
			name:   "production secrets",
			mutate: func(c *Config) { c.Environment = "production" },
			want: []string{
				"discord.client_secret", "api.ops_token", "api.metrics_token",
				"discord.oauth_redirect_uri", "auth.cookie_secure",
			},
		},
		{
			name: "browser boundary",
			mutate: func(c *Config) {
				c.Environment = "staging"
				c.API.CORSOrigins = nil
				c.API.TrustedProxies = []string{"not-a-proxy"}
				c.Auth.CSRFCookieName = c.Auth.SessionCookieName
			},
			want: []string{
				"api.cors_origins", "auth.cookie_secure", "api.trusted_proxies",
				"auth.csrf_cookie_name",
			},
		},
		{
			name: "inexact origins",
			mutate: func(c *Config) {
				c.API.CORSOrigins = []string{"https://*.example.com", "https://example.com/app", "example.com"}
			},
			want: []string{"*.example.com", "example.com/app", `"example.com"`},
		},
		{
			name: "production complete",
			mutate: func(c *Config) {
				c.Environment = "production"
				c.Auth.CookieSecure = true
				c.Discord.ClientSecret = "client-secret"
				c.Discord.OAuthRedirectURI = "https://dashboard.example.com/auth/callback"
				c.API.OpsToken = "ops-secret"
				c.API.MetricsToken = "metrics-secret"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := validConfig()
			test.mutate(&cfg)
			err := cfg.Validate()
			if len(test.want) == 0 {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Validate() = nil, want error")
			}
			for _, key := range test.want {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("error does not mention %s: %v", key, err)
				}
			}
		})
	}
}

func TestLimitText(t *testing.T) {
	var l Limit
	if err := l.UnmarshalText([]byte("120 / 1m")); err != nil {
		t.Fatal(err)
	}
	if l != (Limit{Max: 120, Window: time.Minute}) || l.String() != "120/1m0s" {
		t.Fatalf("got %v", l)
	}
	for _, bad := range []string{"", "120", "x/1m", "1/x"} {
		if err := l.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("UnmarshalText(%q) = nil, want error", bad)
		}
	}
}

// TestExampleFile keeps quack.example.toml loadable and its values in step
// with Default.
func TestExampleFile(t *testing.T) {
	cfg, err := load("../../quack.example.toml", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	want.API.TrustedProxies = []string{}
	want.Discord.OAuthRedirectURI = cfg.Discord.OAuthRedirectURI
	want.Database.DSN = cfg.Database.DSN
	want.Redis.URL = cfg.Redis.URL
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("example differs from defaults:\n got  %+v\n want %+v", cfg, want)
	}
}
