# Quack

**Consistent, customizable moderation for Discord.**

> Admins define the server's moderation rules. Moderators apply those rules.
> Quack chooses and carries out the configured result.

Instead of moderators picking `/warn`, `/timeout`, or `/ban` and guessing at
durations, admins describe their rules once as templates. A moderator picks the
rule that was broken, and Quack looks at the member's history, chooses the
right escalation level, records the case, carries out the action, and tells
the member, the same way every time.

## Features

- **Templates with escalation.** One template per kind of problem (spam,
  harassment, advertising). Levels escalate by case count, with an optional
  decay window, and each level runs at most one timeout, kick, or ban.
- **Cases that stay honest.** Every case snapshots the template version,
  reason, context, and evidence it used. Mistakes are voided, never deleted.
- **Evidence capture.** Grab a live message with a context action or paste a
  link; Quack snapshots it and copies attachments to a staff-only channel.
- **Appeals.** Members sign in with Discord to see their cases and appeal.
  Accepting an appeal voids the case and lifts its timeout or ban.
- **Audit log.** A permanent, searchable record of every meaningful change,
  optionally mirrored to a staff channel.
- **Safe actions.** Discord permissions and role hierarchy are checked on
  every request; failed actions retry when safe and otherwise wait for staff.
- **Optional modules.** Tickets, general logging, and honeypots, each enabled
  per server.

## Repository

| Path | What it is |
| --- | --- |
| `apps/backend` | Go module. One `quack` binary runs the bot, the dashboard API, the workers, migrations, and the v4 import. |
| `apps/dashboard` | Vite+ React dashboard, served by a small Go program that proxies `/api` to the backend. |
| `contracts/http/openapi.yaml` | The HTTP API contract, generated from the Go route table. |
| `docs/design.md` | How the whole system works. The source of truth. |

## Quick start

You need Go, Bun, Docker, and a Discord application for development.

```sh
cp .env.example .env        # fill in the QUACK_DISCORD_* values
docker compose up -d        # MySQL and Redis
(cd apps/backend && go run ./cmd/quack serve)
(cd apps/dashboard && bun install && bun run dev)
```

Open <http://localhost:3000>. The API listens on `:8080` and the dashboard
proxies `/api` to it. Add
`http://localhost:3000/api/auth/discord/callback` to your Discord
application's OAuth2 redirects.

Run tests with `go test ./...` in `apps/backend` and `bun run test` in
`apps/dashboard`.

## Documentation

[`docs/design.md`](docs/design.md) covers the product rules, architecture,
configuration, development, testing, migrations, and operations. Contributors
and agents should also read [`AGENTS.md`](AGENTS.md).

## License

[MIT](LICENSE)
