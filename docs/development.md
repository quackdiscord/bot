# Development

## Setup

You need Go (the version in `apps/backend/go.mod`), Docker for MySQL and
Redis, and a Discord application for development, separate from production.

1. Copy `.env.example` to `.env` and fill in `QUACK_DISCORD_TOKEN`,
   `QUACK_DISCORD_APP_ID`, and `QUACK_DISCORD_CLIENT_SECRET`. Setting
   `QUACK_DISCORD_COMMAND_GUILD_ID` to a test guild makes command changes
   apply instantly.
2. Start MySQL and Redis: `docker compose up -d`.
3. Run Quack from `apps/backend`:

   ```sh
   set -a; source ../../.env; set +a
   go run ./cmd/quack serve
   ```

   Or run `air` from `apps/backend`. It loads `.env` and rebuilds on change
   (`apps/backend/.air.toml`).

The API listens on `http://localhost:8080`. The dashboard dev server
(`apps/dashboard`, `bun run dev`) runs on `http://localhost:3000`, which the
dev CORS defaults allow.

To run Quack in Docker too: `docker compose --profile app up --build`. The app
container reads `.env` and points at the `mysql` and `redis` services.

Settings can also go in a TOML file; see [`configuration.md`](configuration.md)
and `apps/backend/quack.example.toml`.

## Commands

Run Go commands from `apps/backend`. `go.work` at the repo root covers the one
module.

| Command | What it does |
| --- | --- |
| `go run ./cmd/quack serve` | Run the bot, API, and workers. `serve` is the default. |
| `go run ./cmd/quack migrate up` | Apply pending migrations and exit. |
| `go run ./cmd/quack migrate down` | Roll back the newest migration. Needs `-drop-all` for the baseline. |
| `go run ./cmd/quack import-v4 ...` | Import v4 history; see [`v4-import.md`](v4-import.md). |
| `go run ./cmd/quack help` | Usage. `quack <command> -h` lists a command's flags. |
| `gofmt -l .` | List unformatted files; `gofmt -w <file>` fixes one. |
| `go vet ./...` | Vet. |
| `go test ./...` | All tests; see [`testing.md`](testing.md). |
| `go build ./cmd/quack` | Build the binary. |
| `staticcheck ./...` | Optional, if you have it installed. |

CI (`.github/workflows/go.yml`) builds `./cmd/quack` and runs `go test ./...`
from `apps/backend`.

## Where things go

The package map and request flows are in
[`architecture.md`](architecture.md). In short:

- **Business rules** go in `internal/quack`, behind `quack.Services`. Adapters
  stay thin.
- **Storage** goes in `internal/store`. Schema changes are a new migration in
  `store/migrate.go` plus the record structs in `store/schema.go`; see
  [`migrations.md`](migrations.md).
- **HTTP routes** go in `internal/api/routes.go`. Handlers sit next to their
  topic (`cases.go`, `appeals.go`, and so on). The JSON contract is
  `contracts/http/swagger.yaml`. Nothing generates it anymore, so update it by
  hand when a response changes.
- **Discord commands and components** go in `internal/discord`. Custom IDs
  are part of messages already posted in Discord, so don't rename them.
- **Background loops** are registered with `Worker.Every` in
  `internal/app/app.go`.
- **Module features** go in the module's own package under
  `internal/modules/`, wired in `internal/app/app.go`.
- **Settings** go in `internal/config` (struct, default, validation), plus
  `quack.example.toml` and [`configuration.md`](configuration.md).

`Legacy/` is the v4 bot. It is not built or run by v5; leave it alone.

## Code style

Follow [Google Go style](https://google.github.io/styleguide/go/) and
[Go doc comments](https://go.dev/doc/comment). Keep comments short, write them
for a human, and say why something exists rather than restating its name.
`AGENTS.md` has the rules agents follow.
