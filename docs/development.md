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
   go run ./cmd/quack serve
   ```

   Or run `air` from `apps/backend`. It loads `.env` and rebuilds on change
   (`apps/backend/.air.toml`).

The API listens on `http://localhost:8080`. Then start the dashboard from
`apps/dashboard`:

```sh
bun install
bun run dev
```

It runs on `http://localhost:3000` and proxies `/api` to the API, so sign in
there. Your Discord application needs
`http://localhost:3000/api/auth/discord/callback` as an OAuth2 redirect, and
`QUACK_DISCORD_OAUTH_REDIRECT_URI` must match it. See
[`dashboard.md`](dashboard.md) and the dashboard's README for its commands.

To run Quack in Docker too: `docker compose --profile app up --build`. The app
container reads `.env` and points at the `mysql` and `redis` services.

Settings can also go in a TOML file; see [`configuration.md`](configuration.md)
and `apps/backend/quack.example.toml`.

## Commands

Run Go commands from `apps/backend`. `go.work` at the repo root covers it and
the dashboard server in `apps/dashboard`.

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
| `go generate ./...` | Regenerate `contracts/http/openapi.yaml` (and the Discord icon catalog, which needs Node). `go run ./cmd/openapi` prints the contract to stdout. |
| `staticcheck ./...` | Optional, if you have it installed. |

CI (`.github/workflows/go.yml`) builds `./cmd/quack` and runs `go test ./...`
from `apps/backend`. `.github/workflows/dashboard.yml` checks that the
dashboard's API types match the contract, then lints, tests, and builds the
app and its Go server.

## Where things go

The package map and request flows are in
[`architecture.md`](architecture.md). In short:

- **Business rules** go in `internal/quack`, behind `quack.Services`. Adapters
  stay thin.
- **Storage** goes in `internal/store`. Schema changes are a new migration in
  `store/migrate.go` plus the record structs in `store/schema.go`; see
  [`migrations.md`](migrations.md).
- **HTTP routes** go in `internal/api/routes.go`. Handlers sit next to their
  topic (`cases.go`, `appeals.go`, and so on). Each route passes an `api.Doc`
  (or `modules.Doc` for module routes) naming the Go types its handler reads
  and writes; use named types, not `map[string]any` or anonymous structs, so
  the schema is real. The JSON contract, `contracts/http/openapi.yaml`, is
  generated from those docs: run `go generate ./...` from `apps/backend`
  after changing a route or any type it reads or writes, then `bun run api`
  in `apps/dashboard` to update the dashboard's types, and commit both.
  `go test ./...` fails while the file is stale (use `-count=1` if
  only the YAML changed, since the test cache does not track files outside
  the module). Never edit the file by hand.
- **Discord commands and components** go in `internal/discord`. Custom IDs
  are part of messages already posted in Discord, so don't rename them.
- **Background loops** are registered with `Worker.Every` in
  `internal/app/app.go`.
- **Module features** go in the module's own package under
  `internal/modules/`, wired in `internal/app/app.go`.
- **Settings** go in `internal/config` (struct, default, validation), plus
  `quack.example.toml` and [`configuration.md`](configuration.md).

The v4 bot source is available in Git history. v5 keeps the case-history
importer described in [`v4-import.md`](v4-import.md); it does not need the old
bot source to import an export.

## Code style

Follow [Google Go style](https://google.github.io/styleguide/go/) and
[Go doc comments](https://go.dev/doc/comment). Keep comments short, write them
for a human, and say why something exists rather than restating its name.
`AGENTS.md` has the rules agents follow.
