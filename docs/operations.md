# Operations

How to deploy and run Quack, and what to check when something goes wrong.
Settings are in [`configuration.md`](configuration.md), schema changes in
[`migrations.md`](migrations.md).

## Deploying

- Build the image from `apps/backend/Dockerfile`. It produces a static `quack`
  binary on Alpine, runs as a non-root user, exposes 8080, and runs
  `quack serve` by default.
- Quack needs MySQL 8 and Redis. Redis holds sessions, dedupe claims, rate
  limits, and idempotency records, so give it persistence (Compose runs it
  with `--appendonly yes`).
- Run one `serve` process per Discord application. Quack does not shard, and
  some state (multi-page `/case add` drafts) is in memory.
- `serve` migrates on startup. To migrate separately, run `quack migrate up`
  first. Back up MySQL before a release that adds a migration.
- Staging and production need `discord.client_secret`, `api.ops_token`,
  `api.metrics_token`, an `https` `discord.oauth_redirect_uri`, exact
  `api.cors_origins`, and secure cookies. Startup lists every missing or
  invalid setting at once.
- If Quack sits behind a proxy, list the proxy in `api.trusted_proxies`.
  Otherwise forwarded headers are ignored, and per-IP OAuth limits apply to
  the proxy's address.
- On SIGTERM, the HTTP server drains for up to `api.shutdown_timeout`, then
  the gateway, module pools, worker, and storage stop within another
  `api.shutdown_timeout`. Set the platform's termination grace period above
  twice that value.

## Health and monitoring

| Endpoint | Auth | Use |
| --- | --- | --- |
| `GET /livez` | none | Liveness. Always 200 while the process serves, so a dependency outage does not cause a restart loop. |
| `GET /readyz` | none | Readiness. 503 unless MySQL, Redis, the Discord gateway, the action queue, the migration ledger, and every action capability are ready. The body lists each check, and `migration` shows the schema version. |
| `GET /status` | none | Connectivity and latency for Discord, Redis, and MySQL. Always 200. The Compose healthcheck uses it. |
| `GET /metrics` | `X-Quack-Metrics-Key` | Prometheus text. 404 when `api.metrics_token` is unset. |
| `GET /ops/status` | `X-Quack-Ops-Key` | Queue counters, worker settings, execution status counts, the oldest due work, recent failures, and action capabilities across all guilds. 404 when `api.ops_token` is unset. |
| `GET /guilds/{discordGuildID}/ops/status` | ops key, or a guild owner or Administrator session | The same for one guild, plus `guild_health`: missing bot permissions and managed channels. |

Metrics are aggregate counters with no guild, user, or content labels:

- `quack_cases_total`
- `quack_action_attempts_total`, `quack_action_failures_total`,
  `quack_action_retries_total`, `quack_action_retrying_current`
- `quack_action_queue_depth`, `quack_action_queue_failures_total`
- `quack_notifications_total`, `quack_appeals_total`
- `quack_audit_events_total`, `quack_audit_mirror_events_total`
- `quack_optional_module_events_total`

Alert on:

- sustained `/readyz` failure
- a growing queue depth or failure count
- repeated action, notification, or audit mirror failures
- a guild that stays degraded

Logs are structured (JSON outside dev). Each line carries `request_id` and
`correlation_id`, and the same IDs are returned in the `X-Request-ID` and
`X-Correlation-ID` response headers. Logs and audit metadata carry IDs and
outcomes, never message content, tokens, or raw Discord errors.

## Security posture

- **Live authorization.** Every guild request and every Discord interaction
  re-reads the caller's permissions from Discord. Cached staff records are for
  attribution only, so losing a role takes effect on the next request.
- **Sessions.**
  - The session is stored server-side in Redis; Discord tokens never reach the
    browser.
  - The session cookie is HttpOnly and SameSite=Lax, and Secure outside dev.
  - The CSRF cookie is readable by the dashboard, which echoes it in
    `X-CSRF-Token` on writes.
  - With secure cookies, the OAuth state cookie uses the `__Host-` prefix.
  - `POST /auth/logout-all` revokes every session for the user.
- **Browser boundary.** CORS allows only the exact configured origins and
  rejects any other `Origin`. Cookie-authenticated writes need a matching
  `Origin` and CSRF token. Responses carry a deny-all CSP and `no-store`.
- **Fail closed.** If Redis is down, rate limits, idempotency, and Discord
  interaction dedupe refuse work (503 or a dropped interaction) instead of
  skipping the check.
- **Shared secrets.** The ops and metrics keys are compared in constant time.
- **History.** Cases and audit entries are never deleted by normal operation.
  The store refuses updates and deletes on `audit_log_entries`.
- **Errors.** API errors use a fixed envelope with stable codes. Discord and
  storage error text is never returned or stored verbatim.

## When things go wrong

Do not edit `case_action_executions`, `case_notifications`, the audit log,
the migration ledger, or Redis idempotency keys by hand. The staff controls
re-check permissions and keep the fencing intact.

- **MySQL down.** Readiness fails and work waits. Restore the same database.
  If you restore from a backup taken before some Discord actions ran, compare
  the action attempts with the guild's Discord audit log before any staff
  retry.
- **Redis down.** Dashboard writes and logins fail with 503, and Discord
  interactions are dropped. Restore Redis rather than bypassing it. Durable
  cases and actions are in MySQL and are not affected. Sessions lost from
  Redis just mean signing in again.
- **Discord down or rate limiting.** Pending work stays in MySQL. Failures that
  Discord definitely rejected retry on their own, within the action's
  `max_retries`. Uncertain failures, and any action whose lease expired
  mid-attempt, go to the staff review queue.
- **Failed actions.** Staff review them in the dashboard
  (`/guilds/{discordGuildID}/action-failures`) or with `/case failures`, then
  retry, dismiss, or void the case. A retry re-runs the live permission and
  hierarchy checks.
- **A guild is degraded.** Check `guild_health.reasons` on the guild ops
  status. Usually the bot lost a permission or the evidence channel was
  deleted. The evidence channel is recreated when Discord reports the deletion
  and re-checked on the next `GuildCreate`.
- **Leaked token or secret.** Rotate the bot token or client secret in your
  secret store and redeploy. Rotate `api.ops_token` and `api.metrics_token` the
  same way. Revoke affected dashboard sessions with `POST /auth/logout-all`.
- **Bad migration.** Follow [`migrations.md`](migrations.md#failure-and-recovery).

## Before the first real release

The code has not yet been rehearsed in a live Discord guild. Before launch, use
a non-production guild and application to check:

- **Install.** Starter template and notice, evidence channel creation and
  repair, and leave and rejoin.
- **Permissions.** Owner, Administrator, Manage Guild, Moderate Members, a
  former staff member, and the hierarchy, bot, self, and owner target checks.
- **Cases.** Create warning-only, timeout, kick, and ban cases from Discord
  and the dashboard. Check the public result, evidence, the single DM,
  departed-member access, appeals and reversals, the audit mirror, and
  failure retry and dismiss.
- **Modules.** Tickets, general logging, and honeypot, each on its own.
- **Shutdown.** SIGTERM while work is in flight, then confirm nothing runs
  twice after restart.

See the known gaps at the end of [`architecture.md`](architecture.md#known-gaps).
