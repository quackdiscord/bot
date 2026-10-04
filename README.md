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
- `apps/dashboard`: the web dashboard. A Vite+ React app styled with CSS Modules,
  served by a small Go program that also proxies `/api` to the backend. See
  its [README](apps/dashboard/README.md).
- `contracts/http/openapi.yaml`: the HTTP API contract between the backend and
  the dashboard, generated from the Go route table with `go generate ./...`
  in `apps/backend`.
- `Legacy/`: the Quack v4 bot, kept for reference. Not part of v5.
- `docs/`: maintainer documentation. Start at [`docs/README.md`](docs/README.md).

## Quick start

You need Go, Bun, Docker, and a Discord application for development.

```sh
cp .env.example .env        # then fill in the QUACK_DISCORD_* values
docker compose up -d        # MySQL and Redis
cd apps/backend && go run ./cmd/quack serve
cd apps/dashboard && bun install && bun run dev
```

Open `http://localhost:3000`. The API listens on `http://localhost:8080`;
the dashboard proxies `/api` to it. Add
`http://localhost:3000/api/auth/discord/callback` to your Discord
application's OAuth2 redirects. Run `go test ./...` from `apps/backend` to
test. [`docs/development.md`](docs/development.md) has the
rest.

## Docs

- [Architecture](docs/architecture.md): packages, request flows, the action
  engine, appeals, the API, and the modules.
- [Dashboard](docs/dashboard.md): how the web app is served, signs in, and
  talks to the API.
- [Configuration](docs/configuration.md): every setting and its `QUACK_*`
  variable.
- [Development](docs/development.md) and [testing](docs/testing.md).
- [Migrations](docs/migrations.md), [operations](docs/operations.md), and the
  [v4 import](docs/v4-import.md).
