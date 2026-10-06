# Agent Guidelines

## What Quack is

Quack is a Discord moderation bot built around one rule:

> Admins define the server's moderation rules. Moderators apply those rules.
> Quack chooses and carries out the configured result.

Admins build **templates** (one per kind of problem, not per punishment) with
escalation **levels** keyed on case count. A moderator applies a template to a
member; Quack counts that member's valid cases for the template, picks the
level, records the **case**, runs its single timeout, kick, or ban
**action**, sends at most one notification, and writes the **audit log**.
Members can **appeal** from the dashboard. Discord and the dashboard share the
same behavior; Discord stays the authority for permissions. Data never crosses
guilds, cases snapshot the template version they used, and cases and audit
entries are voided or appended to, never deleted.

## The system is documented in `docs/design.md`

`docs/design.md` is the source of truth for the whole system: product rules,
architecture, configuration, and operations. Read it before making changes.
When code and the design disagree, treat it as drift and resolve it
deliberately. Any change that affects behavior, configuration, APIs, or
anything else the docs describe must update `docs/design.md` (and any other
affected docs) in the same commit.

## Packages are lego pieces

Each package is an independent piece with a narrow interface, so pieces can be
swapped, recombined, or tested alone.

- `apps/backend` is one Go module. `internal/quack` is the moderation domain
  and imports no infrastructure.
- `internal/store` (MySQL, Redis), `internal/discord`, `internal/api`,
  `internal/worker`, and `internal/modules/*` are adapters. They depend on the
  domain and on small interfaces, not on each other's implementation details.
- Only `internal/app` wires pieces together for `cmd/quack`.
- `apps/dashboard` is a Vite+ React app plus a small Go server that proxies
  `/api` to the backend; its README has the frontend conventions.

Keep new code in this shape: define the interface where it is consumed, keep
dependencies pointing inward, and don't reach across adapters.

## Go style

- Follow [Google Go style](https://google.github.io/styleguide/go/) and
  [Go doc comments](https://go.dev/doc/comment).
- Every exported identifier gets a doc comment that starts with its name;
  unexported ones get one when it helps. Every package has a package comment.
- Keep comments short and human: say what something is for and why, including
  invariants, side effects, and lifecycle. Keep them accurate when behavior
  changes.

## Commits

- Never add a `Co-Authored-By` trailer or any other co-author attribution.
- Docs changes ship in the same commit as the code they describe.
- Don't commit generated binaries or temporary files; preserve unrelated
  user changes.

## Verification

- Run `gofmt` on changed Go files, and run Go commands from `apps/backend`.
- Run the narrowest relevant tests, then `go test ./...` when possible.
- Dashboard changes: `bun run check`, `bun run test`, and `bun run build` in
  `apps/dashboard`. After changing an API route or type, run
  `go generate ./...` in `apps/backend`, then `bun run api` in
  `apps/dashboard`.

## tmux

Existing tmux sessions, windows, and panes belong to the user. Check for an
already-running server or watcher before starting one. Never kill, restart,
interrupt, or send input to a tmux process, or change layouts or config,
without explicit permission.
