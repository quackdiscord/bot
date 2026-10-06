# Quack dashboard

The web dashboard for Quack v5: a React single-page app built with
[Vite+](https://viteplus.dev), styled with CSS Modules, and
served by a small Go program in this directory.

- Staff moderate their servers: cases, appeals, failed actions, the audit
  log, rules, settings, and the optional modules.
- Members sign in to see their own cases and appeal them. Quack's DMs link to
  `/guilds/{guildId}/cases/{caseId}/appeal`.

It's built to sit comfortably next to Discord without copying it: a calm
dark palette with Quack's duck yellow as the accent, rounded surfaces, quick
motion, and Quack's own Signals icon set (`assets/icons/quack`), the same
icons the bot uses in its messages.

## How it runs

```
browser ──> quack-dashboard (Go, :3000) ──/api/*──> quack serve (:8080)
                 │
                 └── everything else: the built app, falling back to index.html
```

The Go server (`main.go`, `server.go`) embeds the built app, serves it with
long-lived caching for hashed assets, and reverse-proxies `/api/*` to the
backend with the prefix stripped. The browser only ever talks to one origin,
so the backend's host-only session and CSRF cookies just work and no CORS is
involved. In development `vp dev` does the same proxying.

## Development

You need [Bun](https://bun.sh) (or npm) and Go. Start the backend first (see
[`docs/design.md`](../../docs/design.md#development)), then:

```sh
bun install
bun run dev        # http://localhost:3000, proxies /api to localhost:8080
```

Set `QUACK_DASHBOARD_API_URL` to proxy somewhere else. The backend's
`.env` needs the dashboard as its OAuth redirect and allowed origin, which
`.env.example` already sets up:

```sh
QUACK_DISCORD_OAUTH_REDIRECT_URI=http://localhost:3000/api/auth/discord/callback
QUACK_API_CORS_ORIGINS=http://localhost:3000
```

## Commands

| Command         | What it does                                                                                                           |
| --------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `bun run dev`   | Dev server with hot reload.                                                                                            |
| `bun run build` | Production build into `dist/app`.                                                                                      |
| `bun run check` | Format check, lint (Oxlint, type-aware), and type check. `vp check --fix` formats.                                     |
| `bun run test`  | Unit tests (Vitest).                                                                                                   |
| `bun run api`   | Regenerate `src/api/schema.gen.ts` from `contracts/http/openapi.yaml`. Run it after the backend's `go generate ./...`. |
| `go run .`      | Serve `dist/app` and proxy `/api`, as in production. Build first.                                                      |
| `go test ./...` | Test the Go server.                                                                                                    |

## Layout

```
src/
  main.tsx          QueryClient, router, global error handling
  api/              typed client (openapi-fetch), query options, directory lookups
  routes/           TanStack Router file routes (routeTree.gen.ts is generated)
  features/         screens' building blocks, grouped by domain
  ui/               design system primitives (Button, Field, Dialog, DataTable…)
  lib/              pure helpers (formatting, escalation preview, permissions)
  styles/           tokens.css (colors, radii, motion) and global.css
```

### Conventions

- **Data.** Every request goes through `api` in `src/api/client.ts`, typed
  from the generated contract; wrap calls in `unwrap()` so failures become
  `ApiError` with a user-safe `message`. Reads are `queryOptions` factories in
  `src/api/queries.ts` keyed under `keys`, so a write can invalidate a whole
  area (`keys.cases(guildId)`). Route loaders call `ensureQueryData` so
  navigation (and hover preloading) has data before render; components read
  with `useSuspenseQuery` or `useQuery`. The client adds `X-CSRF-Token` and a
  fresh `Idempotency-Key` to every write.
- **Users and channels.** The API returns Discord IDs. Show people with
  `UserChip`/`Mention` (`features/people/User.tsx`), which batch lookups into
  one request per tick. Pick people with `MemberPicker`.
- **Permissions.** `useCan(guildId)` reads the live permissions from
  `GET /guilds/{id}/me`. It only hides what the user can't do; the API checks
  again on every request.
- **Styling.** CSS Modules: each component has a `Name.module.css` next to
  it, imported as `s` and applied with `className={cx(s.a, cond && s.b)}`
  (`~/lib/cx`). Use the custom properties in `src/styles/tokens.css` for
  every color, radius, shadow, and duration; never hard-code a palette
  color. Prefer state attributes (`data-active`, `aria-current`,
  `aria-checked`) over extra classes, and animate with transitions or the
  shared keyframes in `global.css` (`fade-in`, `rise-in`, `spin`,
  `shimmer`). Inline `style` is fine for truly dynamic values (sizes,
  percentages). Shared primitives take a `className` to be adjusted.
- **Motion.** Things that appear should animate in, and things that leave
  should animate out (`usePresence` in `~/lib/usePresence` keeps an element
  mounted for its exit animation). Keep it quick: 120–260ms.
- **Copy.** Short, plain, and specific, like Quack's Discord messages. Say
  what happened and what to do next. Moderation "templates" are called
  **rules** in the UI, as they are in Discord.
- **Mutations.** Show errors inline in dialogs (`onError`); anything else
  falls back to a toast from the global `MutationCache` handler. Confirm
  consequential actions with `ConfirmDialog`.
- **Tests.** Keep logic you test in plain `.ts` files (`draft.ts`,
  `decisions.ts`, `format.ts`) and import them from the components.
