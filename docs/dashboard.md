# Dashboard

The dashboard is `apps/dashboard`: a React single-page app and the small Go
program that serves it. Staff use it to moderate and configure their
servers; members use it to see and appeal their own cases. Its own
[README](../apps/dashboard/README.md) covers day-to-day development and code
conventions. This page covers how it fits with the backend.

## Shape

```
browser ──> quack-dashboard (:3000) ──/api/*──> quack serve (:8080)
                 │
                 └── the built app; any other path falls back to index.html
```

- **App.** Vite+ (`vp`) builds a React app styled with CSS Modules. TanStack
  Router owns the URLs (file routes in `src/routes`, code-split per route),
  TanStack Query owns server state, and TanStack Table renders the lists.
- **Server.** `quack-dashboard` embeds the build, gzips it once at startup,
  serves fingerprinted assets as immutable, sets a strict CSP, and
  reverse-proxies `/api/*` to the API with the prefix removed. It answers
  `GET /healthz` for health checks.
- **One origin.** Because the browser only talks to the dashboard's origin,
  the API's host-only session and CSRF cookies land there, writes pass the
  API's `Origin` check, and no CORS request ever happens.

## Contract

Every API call is typed from `contracts/http/openapi.yaml`.
`bun run api` regenerates `src/api/schema.gen.ts`; CI fails when it is
stale. After changing a route in the backend, run `go generate ./...` in
`apps/backend` and then `bun run api` in `apps/dashboard`.

The dashboard depends on a few routes that exist only for display:

- `GET /guilds/{id}/directory/users?ids=…` resolves user IDs to names and
  avatars. The app batches every ID rendered in one tick into one call.
- `GET /guilds/{id}/directory/members?query=…` searches current members for
  the member picker.
- `GET /guilds/{id}/directory/channels` lists channels for channel pickers.

## Sign-in

1. The app links to `/api/auth/discord/login?redirect_to=<page>`.
2. Discord redirects back to `discord.oauth_redirect_uri`, which must be the
   dashboard's `/api/auth/discord/callback` so the session cookie is set on
   the dashboard's origin.
3. The API redirects to the original page. The app reads
   `GET /api/auth/me` for the user and the CSRF token it echoes on writes.

A request that finds the session gone sends the user to `/login` and back to
where they were afterwards.

## URLs Quack links to

The bot links into the dashboard, so these paths are stable. Every link is
built by `quack.DashboardLinks` (`apps/backend/internal/quack/dashboard.go`);
[`architecture.md`](architecture.md#messages) lists the messages that carry
them.

| Path | Who | From |
| --- | --- | --- |
| `/guilds/{discordGuildID}` | staff | `/help` overview |
| `/guilds/{discordGuildID}/cases` | staff | `/case list`, `/help topic:cases` |
| `/guilds/{discordGuildID}/cases/{caseID}` | staff | `/case view` and evidence pages, the `/case add` receipt, audit mirror entries about a case |
| `/guilds/{discordGuildID}/members/{discordUserID}` | staff | `/case user`, History |
| `/guilds/{discordGuildID}/appeals` | staff | `/help topic:appeals`, audit entries for a new appeal |
| `/guilds/{discordGuildID}/appeals/{appealID}` | staff | the appeal queue post, `/appeals`, audit entries about an appeal |
| `/guilds/{discordGuildID}/failures` | staff | `/case failures` while failures remain |
| `/guilds/{discordGuildID}/rules` | staff | `/help topic:rules`, template import audit entries |
| `/guilds/{discordGuildID}/rules/{templateID}` | staff | `/template` results, audit entries about a rule |
| `/guilds/{discordGuildID}/settings` | staff | `/setup appeals`, `/setup audit`, `/help topic:setup`, settings audit entries |
| `/guilds/{discordGuildID}/modules/{tickets,logging,honeypot}` | staff | the module's `/setup`, its audit entries |
| `/guilds/{guildID}/cases/{caseID}/appeal` | the case's member | appeal submitted, appeal decision and information request DMs (internal guild and case IDs) |

## Configuration

| Variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `QUACK_DASHBOARD_PORT` | `-addr` | `3000` | Listen port (`-addr` takes a full address). |
| `QUACK_DASHBOARD_API_URL` | `-api` | `http://localhost:8080` | Where `/api` is proxied. |
| `QUACK_DASHBOARD_DIR` | `-dir` | embedded build | Serve a build from disk instead. |

The API side needs, for a dashboard at `https://dashboard.example.com`:

```sh
QUACK_API_CORS_ORIGINS=https://dashboard.example.com
QUACK_DISCORD_OAUTH_REDIRECT_URI=https://dashboard.example.com/api/auth/discord/callback
QUACK_API_TRUSTED_PROXIES=<the dashboard's address or network>
```

`api.cors_origins` also tells Quack where its Discord buttons link: the first
`https` origin, or in `dev` the first `http` one too, so local links open
`http://localhost:3000`.
Trusting the dashboard as a proxy keeps per-IP sign-in limits per user
rather than per dashboard; the dashboard forwards `X-Forwarded-For`.

## Deploying

Build the image from the repository root, since the app uses the shared
icon set in `assets/icons`:

```sh
docker build -f apps/dashboard/Dockerfile -t quack-dashboard .
```

It runs as a non-root user on port 3000. `docker compose --profile app up
--build` runs it next to the API. The dashboard is stateless; run as many
as you like.
