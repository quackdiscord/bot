# Quack v5

Quack v5 is a customizable moderation system for Discord. Administrators
define templates and escalation levels; moderators apply a template; Quack
selects the matching level, records the case, and carries out its configured
outcome.

The dashboard and Discord bot are delivery adapters over the same application
core.

The authoritative product definition is [`v5.md`](v5.md). The current backend
does not yet implement every rule in that document; the high-level differences
are tracked in [`docs/v5-scope-drift.md`](docs/v5-scope-drift.md).

## Layout

- `apps/backend`: Go module for the API, Discord bot, workers, migrations, and
  operator commands.
- `apps/dashboard`: reserved dashboard application root; currently empty.
- `contracts/http/openapi.yaml`: generated HTTP API contract.
- `scripts/generate-openapi.sh`: pinned Swaggo contract generator.
- `docs`: architecture, operations, product, and readiness documentation.
- `go.work`: workspace definition for repository Go modules.

## Development

Copy `.env.example` to `.env` and fill in the Discord values, start MySQL and
Redis with `docker compose up -d`, then run:

```sh
set -a; source .env; set +a
go run ./apps/backend/cmd/quack serve
```

`quack` is the only binary: `serve` (the default), `migrate [up|down]`, and
`import-v4`. Settings are described in
[`docs/configuration.md`](docs/configuration.md).

Run the validation suite with:

```sh
go test ./apps/backend/...
go vet ./apps/backend/...
go build ./apps/backend/cmd/quack
```

Regenerate the HTTP contract with:

```sh
./scripts/generate-openapi.sh
```
