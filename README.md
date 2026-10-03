# Quack v5

Quack is a moderation bot for Discord. Admins define templates with escalation
levels, moderators apply a template to a member, and Quack picks the level
from the member's history, records the case, and carries out the configured
timeout, kick, or ban. Members get one notification per case and can appeal
from the dashboard.

> Admins define the server's moderation rules. Moderators apply those rules.
> Quack chooses and carries out the configured result.

[`v5.md`](v5.md) is the product definition. When the code and `v5.md`
disagree, `v5.md` wins.

## Layout

- `apps/backend`: the Go module. One binary, `quack`, runs the Discord bot,
  the dashboard API, and the background workers, and provides the migration
  and v4 import commands.
- `apps/dashboard`: the web dashboard (TanStack Start, Bun). See its
  [README](apps/dashboard/README.md).
- `contracts/http/openapi.yaml`: the HTTP API contract between the backend and
  the dashboard, generated from the Go route table with `go generate ./...`
  in `apps/backend`.
- `Legacy/`: the Quack v4 bot, kept for reference. Not part of v5.
- `docs/`: maintainer documentation. Start at [`docs/README.md`](docs/README.md).

## Quick start

You need Go, Docker, and a Discord application for development.

```sh
cp .env.example .env        # then fill in the QUACK_DISCORD_* values
docker compose up -d        # MySQL and Redis
cd apps/backend
go run ./cmd/quack serve
```

The API listens on `http://localhost:8080`. Run `go test ./...` from
`apps/backend` to test. [`docs/development.md`](docs/development.md) has the
rest.

## Docs

- [Architecture](docs/architecture.md): packages, request flows, the action
  engine, appeals, the API, and the modules.
- [Configuration](docs/configuration.md): every setting and its `QUACK_*`
  variable.
- [Development](docs/development.md) and [testing](docs/testing.md).
- [Migrations](docs/migrations.md), [operations](docs/operations.md), and the
  [v4 import](docs/v4-import.md).
