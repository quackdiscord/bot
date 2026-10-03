# Release Readiness

This document describes the current backend's readiness for controlled
real-guild testing. It records implementation status rather than defining v5
product scope; the product definition lives in [`v5.md`](../v5.md).

## Ops Status

The final application-owned operations contract and failure runbook live in
[`operations-security-v5.md`](operations-security-v5.md). Final evidence and
the current verdict live in [`v5-readiness.md`](v5-readiness.md).

Public process liveness and dependency readiness are separate:

- `GET /livez` reports process liveness without turning dependency outages into
  restart loops.
- `GET /readyz` fails closed unless MySQL, Redis, Discord, migrations, the
  action queue, and required action capabilities are ready.
- `GET /metrics` requires `X-Quack-Metrics-Key` and exposes only aggregate,
  low-cardinality counters.

Public dependency health remains available at:

- `GET /status`

Operational status is guarded:

- `GET /ops/status`
  - Requires `X-Quack-Ops-Key`.
  - The key must match `api.ops_token` (`QUACK_API_OPS_TOKEN`).
  - If `api.ops_token` is unset, this endpoint returns disabled status.
- `GET /guilds/:discordGuildID/ops/status`
  - Allows `X-Quack-Ops-Key` when `api.ops_token` is set.
  - Also allows a normal Discord-authenticated session when the user is the
    Discord guild owner or has Discord `Administrator`.
  - Moderators with only `Moderate Members` are denied.

The ops payload includes queue counters, worker configuration, action execution
status counts, the oldest pending/retry action, recent action failures, and the
runtime action capability table.

Current action capability truth: one case-level notification plus timeout,
kick, ban, timeout removal, and unban adapters are implemented. Live execution
still requires the configured bot and actor permissions, role hierarchy, and a
controlled Discord rehearsal; the repository does not infer a real-guild pass
from isolated adapter tests.

## Request Tracing

HTTP requests may send `X-Request-ID`. Quack validates the value and echoes it
back. Invalid or missing IDs are replaced with generated trace IDs.

Quack also tracks a correlation ID. API, Discord interaction, queue, case,
action, and audit paths carry these IDs so a single moderation flow can be
followed through logs and audit records.

## v4/v5 Coexistence

Run v4 and v5 as separate processes with separate configuration, command sync
settings, and storage. Do not point v5 at v4 tables.

Recommended rollout:

1. Run v5 against its own MySQL schema and Redis database.
2. Register v5 commands in a dev guild first with `DISCORD_COMMAND_GUILD_ID`.
3. Run the versioned v4 historical-case importer in dry-run mode against a
   sanitized export and review only content-free counts/codes.
4. Apply the import twice and prove the second run creates no cases, actions,
   notifications, or escalation history.
5. Verify v4/v5 command scopes do not collide, then remove v4 direct punishment
   commands at cutover rather than retaining a second moderation workflow.
6. Enable v5 command registration for real-guild testing only after the final
   readiness gates pass.

Rollback is command-level and process-level: stop v5, disable or prune only its
scoped commands, and continue using isolated v4 storage. Imported v4 cases are
readable historical records but never contribute to v5 escalation.

## Release Checklist

- Required settings are present: database, Redis, Discord token/app ID, OAuth client
  secret, OAuth callback, and queue sizing.
- `api.ops_token` is set only where Quack developer ops access is needed.
- `go test ./apps/backend/...` passes.
- `/livez` reports process liveness and `/readyz` reports dependency readiness.
- `/status` reports database, Redis, and Discord connectivity.
- `/ops/status` works with the ops key and fails without it.
- A guild owner/Administrator can read guild ops status; a moderator cannot.
- Command sync targets the intended guild/global scope.
- A template can be created with a default level.
- API case creation and Discord `/case add` both create cases through the same
  template-driven flow.
- The single case notification and each configured action succeed in an
  explicitly authorized controlled test.
- Safe retry, ambiguous failure, dismissal, voiding, reversal, restart recovery,
  and duplicate protection are visible in case detail, audit, metrics, and ops.

Relevant files:

- `apps/backend/internal/httpapi/middleware/request.go`
- `apps/backend/internal/httpapi/routes/ops.go`
- `apps/backend/internal/quack/actions.go`
- `apps/backend/internal/quack/ops.go`
- `apps/backend/internal/quack/trace.go`
- `apps/backend/internal/store/ops.go`
