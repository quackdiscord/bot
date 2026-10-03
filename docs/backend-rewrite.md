# Backend rewrite

This is the working plan for simplifying `apps/backend`. It is the brief every
agent works from. When the rewrite lands, the useful parts move into
`docs/architecture.md` and this file is deleted.

## Goal

Keep the core: templates, escalation, cases, durable actions, notifications,
appeals, and audit. Delete or merge everything around it until the backend is
small enough to read in an afternoon. Modernize while we're in there. The code
should look like a different, calmer codebase when we're done.

Targets: about 90 non-test Go files (down from 220), with no file over ~500
lines, and every package explainable in one sentence.

## Fixed constraints

- **The dashboard is out of scope.** The HTTP API contract must not change:
  route paths, methods, request and response JSON, status codes, the error
  envelope, cookie names, the `X-CSRF-Token` and `Idempotency-Key` headers, and
  the OAuth flow. Handler tests are the guard; port them, don't drop them.
- **The Discord surface stays the same:** the `/case` command tree, its options,
  custom IDs for buttons and modals, and embed content.
- **There is no production v5 database.** Squash the schema freely.
- **v4 import stays** (`quack import-v4`). `Legacy/` is untouched.
- **All three modules stay:** tickets, general logging, honeypot.

## Decisions

| Area | Decision |
| --- | --- |
| Config | koanf. Defaults in code, then an optional TOML file, then `QUACK_*` env vars. Durations are Go duration strings. |
| HTTP | stdlib `net/http` with Go 1.22+ `ServeMux` patterns. Gin is removed. |
| Database | GORM + MySQL stays, with one record struct per table and one baseline migration. SQLite is for tests only. |
| Binaries | One binary: `quack serve`, `quack migrate [up\|down]`, `quack import-v4 ...`. Storage verification is deleted. |
| Go | Bump the `go` directive to the installed toolchain. Use the modern stdlib (`slices`, `maps`, `min`/`max`, range-over-int, `errors.Join`, `log/slog`). |

## Target layout

```
apps/backend/
  cmd/quack/main.go         serve | migrate | import-v4
  internal/
    config/                 koanf loading and validation
    quack/                  the domain: types, ports, services, escalation, enforcement
    store/                  GORM + Redis implementation of the quack ports
    discord/                Discord adapter: REST client, interaction router, /case, embeds
    api/                    net/http API: server, middleware, handlers
    worker/                 action/notification queue and poller, background loops
    modules/                module registry
    modules/tickets/        each module owns its domain, storage, Discord, and HTTP glue
    modules/logging/
    modules/honeypot/
    v4import/
    app/                    composition root: build everything, run it, shut it down
    testutil/
```

Package renames: `discordbot` becomes `discord`, `httpapi` becomes `api`,
`workqueue` becomes `worker`, `runtime` becomes `app`, and
`modules/generallogging` becomes `modules/logging`. `quack/model`,
`quack/idutil`, and `quack/actionmods` fold into `quack`. `moduleintegration`
and `internal/logging` disappear.

Dependency direction: `app` imports everything. `api`, `discord`, `worker`, and
`modules/*` import `quack`. `store` imports `quack` and the module packages it
migrates. `quack` imports no infrastructure: no config, discordgo, gorm, redis,
or net/http.

## Style

Follow [Google Go style](https://google.github.io/styleguide/go/) and
[Go doc comments](https://go.dev/doc/comment).

**Comments**
- Write comments for a human reader. Say what something is for and why it
  exists. Don't restate the name or narrate the code.
- Every exported identifier gets a doc comment that starts with its name and is
  written in full sentences. Unexported identifiers get one only when it helps.
- Each package has exactly one package comment.
- Delete the current boilerplate, for example "encapsulates the X rule so
  callers share one consistent package implementation" or "groups the X state
  used to keep this package's responsibilities explicit". Rewrite or remove it.
- Inline comments are rare and explain *why*: an invariant, a race, a Discord
  quirk.

Good:

```go
// selectLevel picks the highest level whose threshold the member has reached.
// The count includes the case being created, so a threshold of 3 fires on the
// member's third case.
```

Bad:

```go
// selectTemplateLevel selects template level according to persisted state and retry policy.
```

**Code**
- Group files by concept (`case.go`, `escalation.go`, `appeal.go`), not by kind
  (`case_types.go`, `case_responses.go`, `case_validation.go`).
- Names are short and clear, with no stutter: `quack.CaseService`, not
  `quack.QuackCaseService`. Initialisms stay uppercase (`ID`, `URL`, `HTTP`).
  Getters have no `Get` prefix unless the store convention needs one.
- Errors look like `fmt.Errorf("load template: %w", err)`: lowercase, no
  trailing punctuation, handled once. Sentinels are named `ErrX`.
- Interfaces belong to the consumer and stay small. Constructors return
  concrete types. No variadic "optional dependency" parameters.
- `context.Context` is the first parameter and is never stored in a struct.
- No 300-character lines. Break long struct literals and long calls across lines.
- Delete `if s == nil || s.db == nil` guards. Constructors guarantee their
  invariants.
- Tests are table-driven where that fits, use `t.Run` and `t.Helper()`, and
  stick to the stdlib (no assertion libraries). Keep the behavior coverage;
  drop tests that only pin implementation trivia.

## Known fixes to land during the rewrite

1. **Notify-only cases can strand their DM.** The poller must find due
   `case_notifications`, not just due executions (store and worker).
2. **The module toggles are split brain.** The settings API keeps
   `tickets_enabled`, `general_logging_enabled`, and `honeypot_enabled` in its
   JSON, but they read and write `module_configurations`. Drop the
   `guild_settings` columns.
3. **Each `/case` request resolves the staff context once.** Pass it down.
4. **Case creation checks idempotency once,** inside the lock.
5. **Ops auth reuses the normal session middleware and a constant-time key
   compare.**
6. **Business rules leave the store:** the starter policy, reversal
   eligibility, retry safety after lease expiry, and appeal eligibility move
   into `quack`.
7. **The appeal dispatcher, audit mirror, and core `/case` components** are
   wired by `app`, not by module glue.
8. **`Irreversible` is dropped from storage.** Every execution row has it set
   to true, so an expired lease always fails the action for staff review. The
   `irreversible` JSON field stays, derived from the action type.
   `SafeForRetry` stays: template actions set it to true, reversals to false.

## Waves

Each agent works in its own git worktree and finishes with one commit.
`go build ./...`, `go vet ./...`, and `go test ./...` must pass from
`apps/backend`. The orchestrator squash-merges each branch onto `v5/monolith`.

| Wave | Work | Runs alongside |
| --- | --- | --- |
| 1 | **Core:** fold model, idutil, and actionmods into `quack`; consolidate files; dedupe; small ports; move rules up from the store | config |
| 1 | **Config + cmd:** koanf, one binary, delete storage verification | core |
| 2 | **Store:** one struct per table, baseline migration, notification polling, helpers | discord, api |
| 2 | **Discord:** merge `discordbot/*` into `discord`; resolve context once; take over core components | store, api |
| 2 | **API:** Gin to `net/http`; merge `httpapi/*` into `api`; port module routes | store, discord |
| 3 | **Modules + app + worker:** dissolve `moduleintegration`, fix the toggles, build `app`, and write `worker` | none |
| 4 | **Polish:** a comment and style pass per package, docs, `AGENTS.md` | per package |
