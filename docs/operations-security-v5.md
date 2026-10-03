# Quack v5 operations and security runbook

This runbook covers the application-owned operations contract. It does not
authorize changes to deployment resources, Compose, container images, GitHub
workflows, or repository settings.

## Health and metrics

- `GET /livez` is process liveness only. It stays `200` during dependency
  outages so an orchestrator does not create a restart loop.
- `GET /readyz` is fail-closed. It returns `503` unless MySQL, Redis, the
  Discord gateway, the action queue, the migration ledger, and every required
  action capability are ready.
- `GET /guilds/{discordGuildID}/ops/status` reports guild-local degradation
  separately. A degraded guild does not make healthy guilds globally unready.
  Check `guild_health.reasons`, current bot permissions, managed evidence
  channel state, and the durable action backlog.
- `GET /metrics` requires `X-Quack-Metrics-Key`. It emits only aggregate,
  low-cardinality Prometheus counters for cases/escalation selections, action
  attempts/failures/retries/queue state, notifications, appeals, audit mirror,
  and optional modules. It never labels member, guild, content, URL, or payload.

Alert on sustained readiness failure; queue depth or stale running actions;
repeated Discord/action/notification/audit-mirror failures; Redis or MySQL
errors; a migration mismatch/dirty ledger; and repeated per-guild degradation.

## Required configuration

Startup validates configuration before connecting to dependencies and reports
every problem at once. Production and staging require `database.dsn`,
`redis.url`, `discord.token`, `discord.app_id`, `discord.client_secret`, an
exact HTTPS `discord.oauth_redirect_uri`, `identify guilds` OAuth scopes,
`api.cors_origins`, secure cookies, `api.ops_token`, and `api.metrics_token`.
Malformed values and unknown keys fail rather than falling back. See
`configuration.md` for the keys and their `QUACK_*` environment names.

HTTP phase/body/shutdown bounds, each documented rate class, event queue size,
session/state/idempotency TTLs, trusted proxies, and command guild/pruning are
process configuration. Managed evidence/audit channels,
notification introduction/footer branding, retry count, and optional-module
toggles are intentionally per-guild settings, not process-global flags.

## Failure recovery

1. Stop automated retry loops before changing durable state. Keep Quack
   running when only one guild is degraded; isolate the affected guild.
2. Inspect `/readyz`, `/ops/status`, the guild ops response, and trace-linked
   structured logs. Use request/correlation IDs rather than member content.
3. Restore the failed dependency. Do not delete cases, action attempts, audit
   entries, notification outbox rows, appeal events, or idempotency keys.
4. Use the authenticated staff retry/dismiss/void/reversal controls. They
   re-check live permissions and retain fencing/idempotency. Never update action
   rows manually to force execution.
5. Confirm queue depth falls, the original action has one terminal result, and
   notifications remain at most once.

### MySQL outage

Fail traffic out of readiness, restore the same durable database from a tested
backup, and verify the migration ledger before accepting writes. If a restore
point precedes external Discord actions, compare action attempts and Discord
audit history before any manual retry; ambiguous irreversible actions require
manual review, not automated replay.

### Redis outage

Dashboard rate limits/idempotency and Discord interaction dedupe fail closed.
Restore the persistent Redis service; do not temporarily bypass those guards.
Expired sessions require sign-in again. Completed durable cases/actions remain
in MySQL and must not be reconstructed from Redis.

### Discord outage

Keep durable pending work and let readiness fail. Do not classify an ambiguous
kick/ban as safely retryable. After recovery, use normal workers for explicitly
safe retries and staff confirmation for ambiguous or irreversible results.

### Queue backlog or stuck actions

Check queue metrics and `/ops/status`. Repair the dependency causing failures.
The worker lease/fencing path reclaims safe expired work. Use staff retry only
after checking the original attempt; dismiss retains history; voiding changes
case validity but does not erase action history.

### Failed migration and rollback

Startup refuses a dirty, edited, reordered, or incomplete ledger. Preserve a
database backup and the failing logs. Inspect `quack_schema_migrations`, then use
reviewed forward repair (`quack migrate up`), or the migration's declared reversible operation. Forward-only
migrations require restoring the pre-deploy backup and the prior application
version. Never delete ledger rows or run startup `AutoMigrate`.

Production rollback preserves MySQL and persistent Redis. Drain the new
version, stop command synchronization, deploy the prior compatible binary, and
verify migration compatibility before readiness. Do not roll code behind a
forward-only schema. Idempotency keys and action attempts must remain intact so
the prior version cannot repeat an external action.

## OAuth and Discord application recovery

Production requires exact HTTPS OAuth redirects, `identify guilds` scopes,
secure host-only cookies, and the intended bot install permissions/intents.
Rotate a compromised client secret or bot token in the secret manager, revoke
affected sessions, restart through normal deployment, and verify login plus
forced reauthentication. Never put tokens in URLs, logs, support transcripts,
or committed `.env` files.

Command synchronization requires the configured application ID and persistent
Redis cache. Use a test guild for rehearsals. Enable pruning only after the v5
command set is verified; a failed sync blocks startup and should be repaired
before traffic is accepted.

## Graceful shutdown

`api.shutdown_timeout` is a single upper bound for HTTP drain, action queue,
optional-module workers, appeal/audit delivery, Discord close, and dependency
cleanup. On SIGTERM the listener stops accepting work, worker contexts are
canceled, accepted bounded queues drain, and the process reports a timeout as
an error. Size the platform termination grace period above this value.

## Local validation

Run without loading production `.env` files:

```sh
gofmt -w <changed-go-files>
go test ./apps/backend/internal/config ./apps/backend/internal/httpapi/... ./apps/backend/internal/discordbot/interactions ./apps/backend/internal/workqueue ./apps/backend/internal/moduleintegration
go test -race ./apps/backend/internal/httpapi/... ./apps/backend/internal/discordbot/interactions ./apps/backend/internal/workqueue ./apps/backend/internal/moduleintegration
go test ./apps/backend/...
go vet ./apps/backend/...
go build ./apps/backend/cmd/quack
git diff --check
```

With a locally running process, use `apps/backend/scripts/v5-local-ops-smoke.sh`. Real guild
install/OAuth/command sync, production backup/restore, vulnerability/secret
scanners, container/Compose, and deployment shutdown remain final rehearsal or
infrastructure gates.
