// Package config loads Quack's settings from code defaults, an optional TOML
// file, and QUACK_* environment variables, in that order.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/toml/v2"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// DefaultFile is the TOML file Load reads when no path is given. It is
// optional: a missing default file is not an error.
const DefaultFile = "quack.toml"

// envPrefix marks the environment variables that override the file.
const envPrefix = "QUACK_"

// Config is everything the process reads at startup. Each field's koanf tag
// is its TOML key; the environment variable is QUACK_<SECTION>_<KEY>.
type Config struct {
	// Environment is dev, test, staging, or production. Anything but dev
	// turns on secure cookies and requires explicit CORS origins and secrets.
	Environment string   `koanf:"environment"`
	API         API      `koanf:"api"`
	Auth        Auth     `koanf:"auth"`
	Discord     Discord  `koanf:"discord"`
	Limits      Limits   `koanf:"limits"`
	Database    Database `koanf:"database"`
	Redis       Redis    `koanf:"redis"`
	Queue       Queue    `koanf:"queue"`
	Log         Log      `koanf:"log"`
}

// API configures the HTTP listener and its protected operator endpoints.
type API struct {
	Port string `koanf:"port"`
	// CORSOrigins lists the exact dashboard origins allowed to make
	// credentialed requests.
	CORSOrigins []string `koanf:"cors_origins"`
	// TrustedProxies lists the proxy IPs or CIDRs whose forwarded client IPs
	// are believed. Empty means no forwarded header is trusted.
	TrustedProxies    []string      `koanf:"trusted_proxies"`
	MaxBodyBytes      int64         `koanf:"max_body_bytes"`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout"`
	ReadTimeout       time.Duration `koanf:"read_timeout"`
	WriteTimeout      time.Duration `koanf:"write_timeout"`
	IdleTimeout       time.Duration `koanf:"idle_timeout"`
	// ShutdownTimeout bounds the whole graceful shutdown, not just HTTP.
	ShutdownTimeout time.Duration `koanf:"shutdown_timeout"`
	// IdempotencyTTL is how long a completed write can be replayed by its
	// Idempotency-Key.
	IdempotencyTTL time.Duration `koanf:"idempotency_ttl"`
	// OpsToken enables GET /ops/status for callers that present it.
	OpsToken string `koanf:"ops_token"`
	// MetricsToken guards the metrics endpoint.
	MetricsToken string `koanf:"metrics_token"`
}

// Auth configures dashboard sessions and their cookies.
type Auth struct {
	SessionCookieName string        `koanf:"session_cookie_name"`
	CSRFCookieName    string        `koanf:"csrf_cookie_name"`
	SessionTTL        time.Duration `koanf:"session_ttl"`
	// StateTTL bounds how long an OAuth login may take.
	StateTTL          time.Duration `koanf:"state_ttl"`
	PostLoginRedirect string        `koanf:"post_login_redirect"`
	CookieSecure      bool          `koanf:"cookie_secure"`
}

// Discord holds the bot credentials, the dashboard OAuth client, and slash
// command sync settings.
type Discord struct {
	Token            string `koanf:"token"`
	AppID            string `koanf:"app_id"`
	ClientSecret     string `koanf:"client_secret"`
	OAuthRedirectURI string `koanf:"oauth_redirect_uri"`
	// OAuthScopes is space-separated, as Discord expects it.
	OAuthScopes string `koanf:"oauth_scopes"`
	// CommandGuildID syncs commands to one guild instead of globally, which
	// Discord applies instantly. Useful while developing.
	CommandGuildID string `koanf:"command_guild_id"`
	// CommandPrune deletes registered commands Quack no longer defines.
	CommandPrune bool `koanf:"command_prune"`
}

// Limits holds the fixed-window rate limit for each class of dashboard
// request. Each is written as "<max>/<window>", for example "120/1m".
type Limits struct {
	OAuth         Limit `koanf:"oauth"`
	MemberRead    Limit `koanf:"member_read"`
	TemplateWrite Limit `koanf:"template_write"`
	CaseCreate    Limit `koanf:"case_create"`
	Retry         Limit `koanf:"retry"`
	Evidence      Limit `koanf:"evidence"`
}

// Limit allows Max requests per Window.
type Limit struct {
	Max    int
	Window time.Duration
}

// UnmarshalText parses "<max>/<window>", such as "20/10m", so a limit fits
// in one TOML value or one environment variable.
func (l *Limit) UnmarshalText(text []byte) error {
	maxText, windowText, ok := strings.Cut(string(text), "/")
	if !ok {
		return fmt.Errorf("rate limit %q: want <max>/<window>, like 20/1m", text)
	}
	limitMax, err := strconv.Atoi(strings.TrimSpace(maxText))
	if err != nil {
		return fmt.Errorf("rate limit %q: bad max: %w", text, err)
	}
	window, err := time.ParseDuration(strings.TrimSpace(windowText))
	if err != nil {
		return fmt.Errorf("rate limit %q: bad window: %w", text, err)
	}
	*l = Limit{Max: limitMax, Window: window}
	return nil
}

// String formats the limit the way UnmarshalText reads it.
func (l Limit) String() string {
	return fmt.Sprintf("%d/%s", l.Max, l.Window)
}

// Database locates MySQL.
type Database struct {
	// DSN is a go-sql-driver/mysql data source name.
	DSN string `koanf:"dsn"`
}

// Redis locates Redis.
type Redis struct {
	URL string `koanf:"url"`
}

// Queue sizes the in-process action queue.
type Queue struct {
	Size    int `koanf:"size"`
	Workers int `koanf:"workers"`
}

// Log configures process logging.
type Log struct {
	// Level is debug, info, warn, or error.
	Level string `koanf:"level"`
}

// sections are the TOML tables that environment variables may set. Other
// QUACK_* variables, such as QUACK_CONFIG or the QUACK_TEST_* test hooks,
// are not configuration keys and are ignored.
var sections = []string{"api", "auth", "discord", "limits", "database", "redis", "queue", "log"}

// Default returns the development defaults. Load replaces the CORS origins and
// cookie security defaults outside dev.
func Default() Config {
	return Config{
		Environment: "dev",
		API: API{
			Port:              "8080",
			CORSOrigins:       []string{"http://localhost:3000", "http://127.0.0.1:3000"},
			MaxBodyBytes:      1 << 20,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   20 * time.Second,
			IdempotencyTTL:    24 * time.Hour,
		},
		Auth: Auth{
			SessionCookieName: "quack_session",
			CSRFCookieName:    "quack_csrf",
			SessionTTL:        7 * 24 * time.Hour,
			StateTTL:          10 * time.Minute,
			PostLoginRedirect: "/",
		},
		Discord: Discord{OAuthScopes: "identify guilds"},
		Limits: Limits{
			OAuth:         Limit{Max: 20, Window: 10 * time.Minute},
			MemberRead:    Limit{Max: 120, Window: time.Minute},
			TemplateWrite: Limit{Max: 30, Window: time.Minute},
			CaseCreate:    Limit{Max: 20, Window: time.Minute},
			Retry:         Limit{Max: 10, Window: time.Minute},
			Evidence:      Limit{Max: 20, Window: time.Minute},
		},
		Queue: Queue{Size: 1000, Workers: 3},
		Log:   Log{Level: "info"},
	}
}

// Load reads the configuration. path names a TOML file; when it is empty,
// QUACK_CONFIG is used, and failing that the optional DefaultFile. Load does
// not validate; call Validate before relying on required settings.
//
// A .env file in the working directory or any parent is read first, so local
// development works without exporting anything; real environment variables
// still win over it.
func Load(path string) (Config, error) {
	dotenv, err := readDotenv()
	if err != nil {
		return Config{}, err
	}
	return load(path, append(dotenv, os.Environ()...))
}

// readDotenv finds the nearest .env walking up from the working directory and
// returns its KEY=VALUE lines. A missing file is not an error.
func readDotenv() ([]string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return nil, nil
	}
	for {
		body, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err == nil {
			return parseDotenv(string(body)), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read .env: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
}

// parseDotenv understands the subset of .env syntax .env.example uses:
// comments, blank lines, an optional "export ", and quoted values.
func parseDotenv(body string) []string {
	var environ []string
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		environ = append(environ, strings.TrimSpace(key)+"="+value)
	}
	return environ
}

// load is Load with the environment passed in, so tests don't depend on the
// process environment.
func load(path string, environ []string) (Config, error) {
	k := koanf.New(".")
	if path == "" {
		path = lookup(environ, envPrefix+"CONFIG")
	}
	required := path != ""
	if !required {
		path = DefaultFile
	}
	if err := k.Load(file.Provider(path), toml.Parser()); err != nil {
		if required || !errors.Is(err, fs.ErrNotExist) {
			return Config{}, fmt.Errorf("read config file %s: %w", path, err)
		}
	}
	envProvider := env.Provider(".", env.Opt{
		Prefix:        envPrefix,
		TransformFunc: envKey,
		EnvironFunc:   func() []string { return environ },
	})
	if err := k.Load(envProvider, nil); err != nil {
		return Config{}, fmt.Errorf("read environment: %w", err)
	}

	cfg := Default()
	if mode := k.String("environment"); mode != "" && mode != "dev" {
		// Production-like environments must name their dashboard origins and
		// never send session cookies over plain HTTP by accident.
		cfg.API.CORSOrigins = nil
		cfg.Auth.CookieSecure = true
	}
	err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(),
				mapstructure.StringToSliceHookFunc(","),
				mapstructure.TextUnmarshallerHookFunc(),
			),
			WeaklyTypedInput: true,
			// Typos in the file or in a known section's env var fail loudly.
			ErrorUnused: true,
		},
	})
	if err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	cfg.API.CORSOrigins = cleanList(cfg.API.CORSOrigins)
	cfg.API.TrustedProxies = cleanList(cfg.API.TrustedProxies)
	return cfg, nil
}

// envKey maps QUACK_DISCORD_APP_ID to discord.app_id. Section names have no
// underscores, so the first one after the section ends it. Empty values are
// treated as unset so Compose's ${VAR:-} passthroughs keep the defaults.
func envKey(name, value string) (string, any) {
	if value == "" {
		return "", nil
	}
	name = strings.ToLower(strings.TrimPrefix(name, envPrefix))
	if name == "environment" {
		return name, value
	}
	section, key, ok := strings.Cut(name, "_")
	if !ok || !slices.Contains(sections, section) {
		return "", nil
	}
	return section + "." + key, value
}

// lookup returns the value of name in an os.Environ-style list.
func lookup(environ []string, name string) string {
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// cleanList trims entries and drops empty ones, so "a, ,b," means [a b].
func cleanList(values []string) []string {
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return slices.DeleteFunc(values, func(v string) bool { return v == "" })
}
