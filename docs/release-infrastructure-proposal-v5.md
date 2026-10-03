# Proposed v5 release infrastructure changes

These are exact proposed changes for an authorized infrastructure owner. QP-H
does not modify any listed release or deployment file.

## GitHub Actions

Add required jobs pinned to the repository's supported Go version:

1. `gofmt`/`git diff --check`, `go vet ./apps/backend/...`, `go test ./apps/backend/...`, and build
   `./apps/backend/cmd/quack`.
2. `go test -race` for HTTP, Discord interactions, queues, core case/action,
   evidence, appeals, and module integration.
3. MySQL and Redis service jobs for migrations, JSON/index/FK/lock/transaction
   behavior, OAuth/session expiry, rate limiting, idempotency, command cache,
   and restart-durable Discord dedupe.
4. `govulncheck ./...`, repository secret scanning, and a rule rejecting
   committed `.env`/credential artifacts.
5. Coverage collection with an agreed non-regression threshold on core
   behavioral packages. Coverage percentage is a release signal, not a proxy
   for the required end-to-end scenarios.

## Container and Compose

Pin the supported Go builder and minimal non-root runtime image; build both
binaries; add read-only filesystem/tmpfs where compatible; declare CPU/memory
limits; expose only the API port; add `/livez` liveness and `/readyz` readiness;
set a termination grace period greater than `api.shutdown_timeout`; and do
not bake secrets into layers.

Compose smoke should provision persistent MySQL and Redis, run migrations,
start Quack, verify `/livez`, `/readyz`, `/status`, authenticated `/ops/status`,
OAuth prerequisites, command sync, and metrics, then send SIGTERM while action,
audit-mirror, appeal, and optional-module work is active. Assert bounded clean
shutdown and no duplicate durable action after restart.

## External authorization gates

Do not make these changes until explicitly authorized. A real Discord test
guild, bot/OAuth credentials, production-like backup/restore environment,
deployment resource ownership, and required-check/repository-setting authority
are also external gates. Record their results in final v5 readiness evidence.
