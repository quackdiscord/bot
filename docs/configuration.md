# Configuration

Quack reads its settings in three layers, each overriding the one before:

1. Defaults in code (`config.Default` in `apps/backend/internal/config`).
2. An optional TOML file. `-config <file>` names it; otherwise `QUACK_CONFIG`;
   otherwise `quack.toml` in the working directory. Only the default file may
   be missing. `apps/backend/quack.example.toml` lists every key.
3. Environment variables named `QUACK_<SECTION>_<KEY>`.

Unknown keys in the file, or in a known section's environment variable, fail
startup, so typos don't pass silently. `quack serve` then validates the result
and reports every problem at once. `migrate` and `import-v4` need only
`database.dsn`.

Quack reads the nearest `.env`, searching up from the working directory, before
the environment. Real environment variables override values from `.env`.

## Environment variables

Take the TOML key, uppercase it, swap the dot for an underscore, and add
`QUACK_`: `discord.app_id` is `QUACK_DISCORD_APP_ID`, `api.cors_origins` is
`QUACK_API_CORS_ORIGINS`. Section names have no underscores, so the first
underscore after the section ends it. The top-level `environment` key is
`QUACK_ENVIRONMENT`.

- Lists are comma-separated: `QUACK_API_CORS_ORIGINS=https://a.example,https://b.example`.
- Durations are Go durations: `15s`, `10m`, `168h`.
- Rate limits are `<max>/<window>`: `QUACK_LIMITS_MEMBER_READ=120/1m`.
- An empty variable counts as unset, so Compose's `${VAR:-}` keeps the default.
- `QUACK_CONFIG` and the test-only `QUACK_TEST_*` variables are not settings.

## Example

```toml
environment = "production"

[api]
cors_origins = ["https://dashboard.example.com"]
trusted_proxies = ["10.0.0.0/8"]

[discord]
# Sign-in goes through the dashboard's /api proxy; see dashboard.md.
oauth_redirect_uri = "https://dashboard.example.com/api/auth/discord/callback"
command_prune = true

[limits]
member_read = "240/1m"
```

Secrets (`discord.token`, `discord.client_secret`, `api.ops_token`,
`api.metrics_token`, `database.dsn`) are best left to the environment.

## Reference

"Prod" means `staging` or `production`.

| Key | Default | Meaning |
| --- | --- | --- |
| `environment` | `dev` | `dev`, `test`, `staging`, or `production`. Anything but `dev` defaults `auth.cookie_secure` to true and `api.cors_origins` to empty. |
| `api.port` | `8080` | HTTP listen port. |
| `api.cors_origins` | localhost:3000 and 127.0.0.1:3000 in dev | Exact dashboard origins allowed to make credentialed requests. Required outside dev; wildcards fail startup. The first `https` origin (in dev, the first `http` or `https` one) is also where Discord messages link to the dashboard; with none, messages carry no dashboard buttons. |
| `api.trusted_proxies` | none | Proxy IPs or CIDRs whose forwarded client IP is trusted. |
| `api.max_body_bytes` | `1048576` | Largest accepted request body. |
| `api.read_header_timeout` | `5s` | HTTP header read limit. |
| `api.read_timeout` | `15s` | Whole-request read limit. |
| `api.write_timeout` | `30s` | Response write limit. |
| `api.idle_timeout` | `1m` | Keep-alive idle limit. |
| `api.shutdown_timeout` | `20s` | How long a graceful shutdown may take in total: the HTTP drain and then every other component. |
| `api.idempotency_ttl` | `24h` | How long a completed write can be replayed by its `Idempotency-Key`. |
| `api.ops_token` | none | Enables `GET /ops/status`, and operator access to `GET /guilds/{discordGuildID}/ops/status`, for callers sending it in `X-Quack-Ops-Key`. Required in prod. |
| `api.metrics_token` | none | Required in `X-Quack-Metrics-Key` to read `GET /metrics`; without it the endpoint is a 404. Required in prod. |
| `auth.session_cookie_name` | `quack_session` | Session cookie name. |
| `auth.csrf_cookie_name` | `quack_csrf` | Double-submit CSRF cookie name. |
| `auth.session_ttl` | `168h` | Dashboard session lifetime. |
| `auth.state_ttl` | `10m` | How long an OAuth login may take. |
| `auth.post_login_redirect` | `/` | Where to send users after login when no target was given. |
| `auth.cookie_secure` | false in dev, true otherwise | Mark cookies `Secure`. Must be true outside dev. |
| `discord.token` | none | Bot token. Required. |
| `discord.app_id` | none | Application ID. Required. |
| `discord.client_secret` | none | OAuth client secret. Required in prod. |
| `discord.oauth_redirect_uri` | none | OAuth callback URL: the dashboard's `/api/auth/discord/callback`, so the session cookie lands on the dashboard's origin. A plain `https` URL in prod. |
| `discord.oauth_scopes` | `identify guilds` | Space-separated OAuth scopes; must include both defaults in prod. |
| `discord.command_guild_id` | none | Sync slash commands to this guild only. Guild commands update instantly. |
| `discord.command_prune` | `false` | Delete registered commands Quack no longer defines. |
| `limits.oauth` | `20/10m` | OAuth login and callback, per client IP. |
| `limits.member_read` | `120/1m` | Dashboard reads, plus the per-actor limit on member, appeal review, and module routes. |
| `limits.template_write` | `30/1m` | Dashboard writes other than case creation, retries, and reversals. |
| `limits.case_create` | `20/1m` | Case creation. |
| `limits.retry` | `10/1m` | Action retries and reversals. |
| `limits.evidence` | `20/1m` | A second limit spent by case creation, which may capture evidence. |
| `database.dsn` | none | MySQL DSN, e.g. `user:pass@tcp(host:3306)/quack?charset=utf8mb4&parseTime=True&loc=Local`. Required. |
| `redis.url` | none | Redis URL, e.g. `redis://host:6379/0`. Required for `serve`. |
| `queue.size` | `1000` | Action queue buffer. |
| `queue.workers` | `3` | Action queue workers. |
| `log.level` | `info` | `debug`, `info`, `warn`, or `error`. Dev logs are colored text; others are JSON. |

[`architecture.md`](architecture.md#http-api) describes how the rate limit classes map to routes.

## Development and production Discord apps

Use a separate Discord application for development and keep its credentials
in your local `.env`. Production credentials live only in the production
environment.

## Docker Compose

`docker compose up -d` starts MySQL and Redis; the `.env.example` storage
addresses point at them from the host. `docker compose --profile app up --build`
also runs the app and the dashboard (on port 3000), reading `.env` and replacing `QUACK_DATABASE_DSN` and
`QUACK_REDIS_URL` with the container hostnames.

## Discord install permissions and intents

The install URL needs the `bot` and `applications.commands` OAuth scopes.

The bot needs:

- View Channel, Send Messages, Embed Links, and Read Message History, for case
  results, evidence capture, the audit mirror, and module channels.
- Moderate Members, Kick Members, and Ban Members, for whichever actions the
  guild's templates use. Case creation is refused up front when the bot lacks
  the permission a level's action needs.
- Manage Channels, for the managed evidence channel and ticket channels.

The guild ops status (`GET /guilds/{discordGuildID}/ops/status`) reports the
guild as degraded when the bot lacks Moderate Members, Kick Members, Ban
Members, or Manage Channels.

Gateway intents are chosen at startup (`internal/app/app.go`). Quack always requests
`Guilds`. It adds more only when at least one guild has the matching module
on, so a module switched on later gets its events after a restart:

- General logging: Guild Members, Guild Moderation, Guild Messages, and
  Message Content.
- Honeypot: Guild Messages.

Guild Members and Message Content are privileged, so enable them in the
Discord developer portal before turning on general logging. Discord also
blanks message text in REST responses for apps without Message Content, which
affects captured evidence.
