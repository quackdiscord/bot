# Architecture

## Runtime

Quack is one Go process started by `quack serve` (`apps/backend/cmd/quack/main.go`),
which loads and validates configuration and sets the logger. The composition root in
`apps/backend/internal/runtime` then opens MySQL and Redis,
migrates the schema, creates the Discord adapter, application services, durable
work scheduler, and HTTP adapter, then shuts them down in reverse order.

Runtime dependencies are constructor-injected. There are no process-global
configuration, database, Redis, queue, Discord session, command, or event
registries.

Guild lifecycle handlers are registered before the Discord gateway opens.
Initial `GuildCreate` events therefore run the idempotent install transaction
before dashboard traffic is needed. `GuildUpdate`, true leave, rejoin, and
channel deletion events refresh or repair durable guild state without deleting
moderation history; temporary Discord unavailability does not deactivate a
guild.

## Boundaries

- `apps/backend/internal/quack` contains transport-independent use cases, domain models,
  repository ports, Discord gateway ports, and queue ports.
- `apps/backend/internal/store` implements repository ports with GORM/MySQL and Redis.
  GORM-tagged migration records remain private to this adapter.
- `apps/backend/internal/httpapi` translates HTTP requests, authentication, cookies, and
  response DTOs into application calls.
- `apps/backend/internal/discordbot` translates Discord interactions into the same
  application calls and owns Discord response rendering and command sync.
- `apps/backend/internal/workqueue` schedules persisted case actions for application
  processing.
- `apps/backend/internal/config` loads defaults, the TOML file, and `QUACK_*` environment variables without mutable global state.

Dependencies point inward: adapters may import the application core, while the
core imports no Gin, DiscordGo, GORM, or Redis packages.

## Live Discord authorization

Discord is the authority for guild access. Every protected dashboard request
and Discord moderation request resolves a fresh guild/actor/bot snapshot through
the `DiscordClient` port. The snapshot contains current membership, guild-level
permission bits, account type, and top-role position. OAuth guild lists,
session-time permissions, interaction permission bits, and `staff_members` rows
never grant access; the staff row is only refreshed attribution/display cache.

The shared core capability map grants full access to the guild owner and
`Administrator`, configuration access to `Manage Guild`, and moderation/case/
audit access to `Moderate Members`. Case preflight additionally requires the
actor and bot action permission and rejects unsafe targets against both role
hierarchies. HTTP and Discord map the same typed authorization errors without
exposing Discord REST failures.

## Moderation flow

Both `POST /guilds/:discordGuildID/cases` and Discord `/case add` call the
same case service. Case creation:

1. Resolves the actor through Discord-derived guild permissions.
2. Locks the guild row inside a unit of work.
3. Loads the selected template and counts matching non-voided history.
4. Selects the highest matching escalation level, then refreshes live target,
   actor, bot, permission, and hierarchy state before any case row exists.
5. Snapshots the selected policy.
6. Allocates the guild case number and writes the case, initial event, action
   executions, and audit row transactionally.
7. Submits a best-effort wake-up hint to the work queue.

The guild lock makes simultaneous cases observe a deterministic history and
receive unique case numbers.

## Guild setup and settings

The guild settings service exposes authorized read, partial update, and
starter-notice acknowledgement contracts to HTTP. Current Discord owner,
`Administrator`, or `Manage Guild` authority is required. Successful writes are
audited in the same transaction; validation failures and denied writes append
failure or denied evidence with request and permission context.

The install transaction creates exactly one active, editable, appealable
`General rule violation` starter template and binds its identity to the guild
settings row. Repeated create/update events and rejoin preserve that identity.
Notice acknowledgement changes only dashboard setup state, never template
availability. The settings boundary stores the future managed-evidence channel
reference, but channel creation, permission checks, and attachment upload remain
the evidence module's responsibility.

## Action scheduling

Persisted `case_action_executions` rows are the source of truth. Immediate
submission reduces latency, but queue saturation cannot lose the action.

The scheduler discovers due pending or retrying cases at startup and every
second in batches of 100. Workers rely on database claim locks to prevent
duplicate execution. Retry timing is represented only by `next_retry_at`; no
per-retry goroutine is required.

Shutdown stops new submissions and polling, closes the job channel outside the
queue mutex, and drains workers.

Current action capability remains unchanged:

- `send_dm`: implemented.
- `timeout_user`, `kick_user`, and `ban_user`: recorded as unsupported
  failures.

## Persistence compatibility

Store-owned schema records preserve the existing table names, columns, indexes,
and JSON columns. Production startup runs an ordered, checksum-tracked migration
registry under a MySQL advisory lock; checksums bind embedded executable
migration source and frozen schema records. It does not call `AutoMigrate`.
Rollback intent is durably recorded before MySQL DDL, and normal startup refuses
an incomplete rollback until the operator reruns its idempotent inverse. The
initial additive migration adopts a current pre-ledger v5 database without
rewriting its records. Plain domain models do not contain GORM tags. Redis
authentication and Discord command-cache key formats are unchanged.

## Delivery adapters

The HTTP and Discord adapters call `quack.Services` directly in-process.
Discord does not call the HTTP server. Existing HTTP routes, response fields,
trace headers, cookies, and Discord `/case add` behavior remain compatibility
contracts.

`Legacy/` is a separate Go module and behavioral reference; it is not part of
the v5 runtime dependency graph.
