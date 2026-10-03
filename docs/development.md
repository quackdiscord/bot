# Development

## Local Workflow

The default process starts everything in one binary: API, Discord session, DB
migrations, Redis-backed storage, and the in-process action queue. See
`apps/backend/cmd/quack/main.go`.

Typical loop:

1. Copy `.env.example` to `.env` and fill in the Discord values.
2. Start MySQL and Redis with `docker compose up -d`.
3. Load `.env` with `set -a; source .env; set +a` (Quack doesn't read it
   itself), then run `go run ./apps/backend/cmd/quack serve`. Or run `air`
   from `apps/backend`, which loads `.env` and rebuilds on change.
4. Exercise API routes on `http://localhost:8080` unless `QUACK_API_PORT` is
   changed.

The Compose defaults expose:

- MySQL on `127.0.0.1:3306`
- Redis on `127.0.0.1:6379`

Copy `.env.example` to `.env` for local defaults, then fill the Discord values.
The local dependency DSNs are:

```sh
QUACK_DATABASE_DSN='quack:quack@tcp(127.0.0.1:3306)/quack?charset=utf8mb4&parseTime=True&loc=Local'
QUACK_REDIS_URL='redis://127.0.0.1:6379/0'
```

Settings can also live in a TOML file; see
[`configuration.md`](configuration.md) and `apps/backend/quack.example.toml`.

To run the app in Docker as well:

```sh
docker compose --profile app up --build
```

The `app` profile uses the internal container hostnames `mysql` and `redis` and
waits for both health checks before starting.

There is no Makefile or task runner in this checkout. The repo is driven
directly through `go` commands and environment variables.

## Monorepo Layout

- `apps/backend` is the Go module and owns the backend container assets.
- `apps/dashboard` is reserved for the dashboard source and is intentionally
  empty.
- `contracts/http/openapi.yaml` is generated from backend annotations.
- `scripts/generate-openapi.sh` regenerates that contract with a pinned Swaggo
  CLI version; do not edit the generated YAML by hand.

## Docker Assets

The container assets are intentionally small:

- `compose.yaml` defines the default local MySQL and Redis services
- `apps/backend/Dockerfile` builds a single static `quack` binary into an Alpine runtime
  image
- `.env.example` lists the `QUACK_*` settings used for local development

Use `docker compose up -d` when you only want the local data services and plan
to run `go run ./apps/backend/cmd/quack serve` on the host.

Use `docker compose --profile app up --build` when you want Compose to run the
Quack process as well. In that mode, the app container uses `mysql` and `redis`
service hostnames instead of `127.0.0.1`.

## Common Commands

Start the app (after loading `.env`):

```sh
go run ./apps/backend/cmd/quack serve
```

Start dependencies:

```sh
docker compose up -d
```

Stop dependencies:

```sh
docker compose down
```

Run the test suite:

```sh
go test ./apps/backend/...
```

Regenerate the HTTP contract:

```sh
./scripts/generate-openapi.sh
```

Apply only database migrations without starting the other adapters:

```sh
go run ./apps/backend/cmd/quack migrate up
```

See [`migrations.md`](migrations.md) before any production forward or rollback
operation.

When you need a stable local cache path on macOS, this repo has previously been
run with:

```sh
GOCACHE=/tmp/quack-go-build-cache go test ./apps/backend/...
```

That cache override is a local convenience, not a code requirement.

## Where To Change Things

- Add or change API endpoints in `apps/backend/internal/httpapi/routes/` and `apps/backend/internal/httpapi/middleware/`.
- Add or change business rules in `apps/backend/internal/quack/`.
- Add or change persistence behavior in `apps/backend/internal/store/`.
- Add or change schema records and enums in `apps/backend/internal/quack/model/schema.go`.
- Add or change Discord command behavior in `apps/backend/internal/discordbot/commands/`.
- Add or change process-level infrastructure in `apps/backend/internal/runtime/` and `apps/backend/internal/workqueue/`.

The main shared service boundary is `quack.Services` in `apps/backend/internal/quack/app.go`. Prefer
putting business behavior there rather than duplicating it in route handlers or
Discord commands.

## Current Maintainability Notes

- The `Legacy/` tree is still present but separate from the v5 runtime. The
  current process entrypoint is `apps/backend/cmd/quack/main.go`, not `Legacy/main.go`.
- The authoritative product definition lives in `v5.md`.
- High-level differences between that definition and the current backend live
  in `docs/v5-scope-drift.md`.
- Development CORS defaults to localhost port `3000`; production requires an
  explicit exact-origin allowlist and fails startup when it is absent.
- Action execution is in-process, not an external worker service.

Relevant files:

- `apps/backend/cmd/quack/main.go`
- `apps/backend/internal/quack/app.go`
- `apps/backend/internal/httpapi/routes/router.go`
- `apps/backend/internal/discordbot/commands/case.go`
- `compose.yaml`
- `apps/backend/Dockerfile`
- `.env.example`
- `v5.md`
- `docs/v5-scope-drift.md`
