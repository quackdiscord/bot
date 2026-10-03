# Agent Guidelines

## Package map

The backend is one Go module in `apps/backend`. `internal/quack` is the
moderation domain and imports no infrastructure. `internal/store` (MySQL and
Redis), `internal/discord`, `internal/api` (`net/http`), `internal/worker`,
and `internal/modules/*` are adapters around it, and `internal/app` wires
everything together for `cmd/quack`. See `docs/architecture.md` for what each
package does and how requests flow; `v5.md` is the product definition.

## Go documentation

- Follow [Google Go style](https://google.github.io/styleguide/go/) and [Go doc comments](https://go.dev/doc/comment).
- Every exported identifier gets a doc comment that starts with its name. Unexported ones get one when it helps the reader.
- Keep comments short and human. Say what something is for and why it exists, including invariants, side effects, and lifecycle, rather than restating the name or narrating the code.
- Keep comments accurate when behavior changes.

## User-owned tmux sessions

- Treat every existing tmux session, window, and pane as user-owned state.
- Before starting a server, watcher, log tail, or other long-running process, check whether the user already has one running in tmux when that session is available.
- Do not kill, restart, interrupt, replace, or send input to a tmux process without the user's explicit permission.
- Do not change pane layouts, active windows, session names, or tmux configuration unless specifically requested.
- Prefer non-invasive inspection. If work requires interacting with an existing tmux process, explain the intended action first and preserve the user's current session state.

## Verification

- Run `gofmt` on changed Go files.
- Run backend Go commands from `apps/backend`.
- Run the narrowest relevant tests, followed by `go test ./...` from `apps/backend` when the environment permits it.
- Preserve unrelated user changes and avoid committing generated binaries or temporary files.
