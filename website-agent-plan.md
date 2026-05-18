# Quack Dashboard Agent Plan

This is the handoff brief for the website agent. It describes the backend surface that exists today, how the Discord bot fits into the product, and what the web dashboard should implement first so Quack v5 can be tested end to end.

Do not treat this as a full backend design document. The frontend only needs enough detail to call the API correctly, show the right guild-scoped UI, and avoid building screens for backend features that do not exist yet.

## Product Context

Quack v5 is moving from a Discord-first moderation bot to an API-first moderation system.

The backend is the source of truth for guild authorization, templates, cases, action execution, and audit records. Discord is an execution surface for moderators. The web dashboard is the admin surface where staff manage templates and review moderation state.

Current working path:

1. User logs into the dashboard with Discord OAuth.
2. Dashboard lists Discord guilds the user can manage.
3. User selects a guild where Quack is installed.
4. Backend resolves the user's guild/staff context and permission map.
5. Authorized staff can create and manage case templates.
6. Authorized staff can create a case from a template through the API.
7. Moderators can also use Discord `/case add` to create template-driven cases.
8. The backend action engine can process safe actions: `record_warning`, `send_dm`, and `write_mod_log`.

## Frontend Assumptions

- Backend default local API base URL is `http://localhost:8080`.
- The backend uses cookie auth. Dashboard fetches must use `credentials: "include"`.
- Backend CORS currently allows `http://localhost:3000` and `http://127.0.0.1:3000`.
- If the website dev server runs on any other port, update backend CORS first or run the website on port `3000`.
- Discord OAuth scopes for the backend login are `identify guilds`.
- Public guild route params use Discord guild IDs, not internal guild ULIDs.
- Template route params use internal template ULIDs returned by template responses.
- Large Discord permission bitfields are returned as strings in responses. Treat them as strings in the UI unless the UI explicitly needs bit math.

## Implemented API Endpoints

### Status

`GET /status`

No auth required. Use this for a lightweight backend status indicator.

Response shape:

```json
{
  "discord": { "connected": true, "username": "Quack", "latency": 42 },
  "redis": { "connected": true, "latency": 1 },
  "database": { "connected": true, "latency": 2 }
}
```

### Auth

`GET /auth/discord/login`

Starts the Discord OAuth flow by redirecting to Discord.

`GET /auth/discord/login?mode=json&redirect_to=/dashboard`

Returns the Discord OAuth URL instead of redirecting. This is useful for SPA-style login buttons.

```json
{
  "auth_url": "https://discord.com/oauth2/authorize?...",
  "state": "01..."
}
```

`GET /auth/discord/callback`

Discord calls this after OAuth. The backend stores the session and sets the auth cookie.

`GET /auth/me`

Requires auth. Returns the logged-in Discord user and session metadata.

```json
{
  "user": {
    "id": "489264179472236557",
    "username": "kyle",
    "global_name": "Kyle",
    "avatar": "avatar_hash",
    "avatar_url": "https://cdn.discordapp.com/avatars/..."
  },
  "session": {
    "id": "01...",
    "expires_at": "2026-05-25T00:00:00Z",
    "last_seen": "2026-05-18T00:00:00Z"
  }
}
```

`POST /auth/logout`

Requires auth. Clears the session cookie. Returns `204`.

### Guild List

`GET /guilds`

Requires auth. Lists Discord guilds the logged-in user can manage. This combines the user's Discord OAuth guild list with the bot's current guild list.

Use this as the dashboard's first guild picker endpoint.

```json
{
  "guilds": [
    {
      "discord_guild_id": "1005778938108325970",
      "name": "Quack's Pond",
      "icon_url": "https://cdn.discordapp.com/icons/...",
      "permission_bits": "1099511627776",
      "is_owner": true,
      "is_administrator": false,
      "can_manage_guild": true,
      "quack_in_guild": true,
      "quack_guild_name": "Quack's Pond"
    }
  ]
}
```

Important UI behavior:

- Show every guild returned by this endpoint.
- Clearly distinguish guilds where `quack_in_guild` is `true` from guilds where Quack is not installed.
- Only enable the v5 management modules for guilds where `quack_in_guild` is `true`.
- For guilds where Quack is not installed, show a placeholder/install CTA. There is no backend invite endpoint yet.

### Guild Staff Context

`GET /guilds/:discordGuildID/me`

Requires auth and requires the bot to be in the guild. This bootstraps or refreshes the guild/staff rows and returns what the current user can do in that guild.

```json
{
  "guild": {
    "id": "01...",
    "discord_guild_id": "1005778938108325970",
    "name": "Quack's Pond",
    "icon_url": "https://cdn.discordapp.com/icons/...",
    "owner_discord_user_id": "489264179472236557",
    "rollout_state": "disabled"
  },
  "staff": {
    "id": "01...",
    "discord_user_id": "489264179472236557",
    "display_name": "Kyle",
    "permission_bits": "1099511627776",
    "is_owner": true,
    "is_administrator": false,
    "disabled": false,
    "last_active_at": "2026-05-18T00:00:00Z",
    "last_seen_permissions": "1099511627776"
  },
  "permissions": {
    "case.create": true,
    "case_template.read": true,
    "case_template.write": true,
    "case_template.delete": true,
    "appeal.review": true,
    "ticket.resolve": true,
    "audit.read": true
  }
}
```

Use `permissions` to show, hide, or disable dashboard modules and actions.

### Case Templates

`GET /guilds/:discordGuildID/templates`

Requires `case_template.read`. Lists non-archived templates in expanded form.

`POST /guilds/:discordGuildID/templates`

Requires `case_template.write`. Creates a template.

`GET /guilds/:discordGuildID/templates/:templateID`

Requires `case_template.read`. Gets one expanded template.

`PATCH /guilds/:discordGuildID/templates/:templateID`

Requires `case_template.write`. Updates a template. The backend replaces actions and escalation rules from the request body and increments the template version.

`DELETE /guilds/:discordGuildID/templates/:templateID`

Requires `case_template.delete`. Archives the template instead of hard deleting it.

Template request shape:

```json
{
  "slug": "spam-warning",
  "name": "Spam Warning",
  "description": "Warns a user for spam.",
  "reason_template": "Please stop spamming in this server.",
  "required_permission_bits": 0,
  "default_severity": "low",
  "default_weight": 1,
  "dm_enabled": false,
  "dm_template": "",
  "enabled": true,
  "actions": [
    {
      "action_type": "record_warning",
      "required_permission_bits": 0,
      "config": {},
      "continue_on_error": false,
      "max_retries": 0,
      "retry_backoff_ms": 0,
      "timeout_ms": 0,
      "idempotency_scope": "case",
      "enabled": true
    }
  ],
  "escalation_rules": []
}
```

Template response shape:

```json
{
  "template": {
    "id": "01...",
    "guild_id": "01...",
    "slug": "spam-warning",
    "name": "Spam Warning",
    "description": "Warns a user for spam.",
    "reason_template": "Please stop spamming in this server.",
    "required_permission_bits": "0",
    "default_severity": "low",
    "default_weight": 1,
    "dm_enabled": false,
    "dm_template": "",
    "enabled": true,
    "version": 1,
    "created_by_discord_user_id": "489264179472236557",
    "updated_by_discord_user_id": "489264179472236557",
    "archived_at": null,
    "actions": [
      {
        "id": "01...",
        "position": 1,
        "action_type": "record_warning",
        "required_permission_bits": "0",
        "config": {},
        "continue_on_error": false,
        "max_retries": 0,
        "retry_backoff_ms": 0,
        "timeout_ms": 0,
        "idempotency_scope": "case",
        "enabled": true
      }
    ],
    "escalation_rules": []
  }
}
```

Supported template action types:

- `record_warning`: supported and safe.
- `send_dm`: supported. Optional config: `{ "message": "..." }`.
- `write_mod_log`: supported. Optional config: `{ "channel_id": "...", "message": "..." }`. If no `channel_id` is provided, backend needs guild settings to have a mod-log channel, but no settings update endpoint exists yet.
- `timeout_user`: accepted by template validation, but execution is intentionally blocked for now.
- `kick_user`: accepted by template validation, but execution is intentionally blocked for now.
- `ban_user`: accepted by template validation, but execution is intentionally blocked for now.

Dashboard guidance:

- Start with a practical form for `record_warning`.
- Add `send_dm` next because it is also supported today.
- Treat timeout/kick/ban as future/disabled action choices until the backend supports guarded irreversible execution.
- Preserve action order in the UI. The backend normalizes action positions from request order.

### Cases

`POST /guilds/:discordGuildID/cases`

Requires `case.create`. Creates a case from an enabled, non-archived template and creates pending action execution rows.

Request:

```json
{
  "template_id": "01...",
  "target_discord_user_id": "123456789012345678",
  "reason_override": "Manual reason shown instead of the template default.",
  "source": "api",
  "context_channel_discord_id": "",
  "context_message_discord_id": "",
  "context_url": "",
  "metadata": {}
}
```

Response:

```json
{
  "case": {
    "id": "01...",
    "guild_id": "01...",
    "case_number": 1,
    "template_id": "01...",
    "template_version": 1,
    "target_discord_user_id": "123456789012345678",
    "moderator_discord_user_id": "489264179472236557",
    "reason": "Manual reason shown instead of the template default.",
    "severity": "low",
    "weight": 1,
    "status": "open",
    "source": "api",
    "actions": [
      {
        "id": "01...",
        "position": 1,
        "action_type": "record_warning",
        "status": "pending",
        "template_action_id": "01...",
        "idempotency_key": "case:...",
        "max_retries": 0,
        "retry_backoff_ms": 0,
        "safe_for_retry": true,
        "irreversible": false
      }
    ]
  }
}
```

Current limitation: there is no case list, case detail, or case timeline read endpoint yet. After creating a case, the dashboard should show the response in a "recently created case" panel and avoid pretending it can reload full history.

## Discord Bot Behavior The Dashboard Should Know

- The bot registers Discord application commands on startup through a command registry.
- Command sync uses hashes so unchanged commands are not re-registered every startup.
- `DISCORD_COMMAND_GUILD_ID` controls whether command sync targets one test guild or global commands.
- `DISCORD_COMMAND_PRUNE=true` allows production/source-of-truth cleanup of remote Discord commands that no longer exist in source.
- Current user-facing Discord command is `/case add`.
- `/case add` accepts a template, target user, and optional reason override.
- Template autocomplete uses the same guild-scoped template service that the API uses.
- Discord command handlers should stay thin. They call shared app services rather than duplicating moderation logic.

Dashboard implication: after a template is created in the dashboard, a moderator should be able to test it in Discord through `/case add` in the same guild.

## Permissions To Use In The UI

Use `GET /guilds/:discordGuildID/me` as the source of truth.

Known permission actions:

- `case.create`: can create cases from templates.
- `case_template.read`: can list and view templates.
- `case_template.write`: can create and edit templates.
- `case_template.delete`: can archive templates.
- `appeal.review`: future appeal review surface.
- `ticket.resolve`: future ticket surface.
- `audit.read`: future audit log read surface.

Owner and Discord administrator users are currently allow-all. Disabled staff are denied.

## Dashboard MVP Plan

### Slice 1: Authenticated Dashboard Shell

Build:

- Login button using `/auth/discord/login?mode=json&redirect_to=/dashboard`.
- Session check using `/auth/me`.
- Logout using `POST /auth/logout`.
- Backend status indicator using `/status`.
- A single dashboard workspace route. Avoid route-per-resource sprawl for the first implementation.

Acceptance criteria:

- Logged-out users see login.
- Logged-in users see their Discord identity.
- Session fetches include cookies.
- Logout clears local UI state.

### Slice 2: Guild Picker And Guild Context

Build:

- Guild list from `GET /guilds`.
- Visual indicator for `quack_in_guild`.
- Selected guild state.
- Guild context panel from `GET /guilds/:discordGuildID/me`.
- Permission-driven module availability.

Acceptance criteria:

- User can see manageable guilds from Discord auth.
- User can select an installed guild and see staff/guild context.
- Non-installed guilds do not show template/case tools.
- Permission-denied modules are visible as unavailable or hidden consistently.

### Slice 3: Template Management

Build:

- Template list.
- Template detail/edit panel.
- Create template form.
- Archive template action.
- Start with one-action `record_warning` templates.
- Allow `send_dm` once the simple path works.

Acceptance criteria:

- User with `case_template.read` can list/view templates.
- User with `case_template.write` can create and update templates.
- User with `case_template.delete` can archive templates.
- Template form sends request bodies matching the backend shape above.
- The UI shows backend validation errors instead of swallowing them.

### Slice 4: Case Creation Test Panel

Build:

- Template selector from current guild templates.
- Target Discord user ID input.
- Optional reason override.
- Submit to `POST /guilds/:discordGuildID/cases`.
- Recent created-case result panel.

Acceptance criteria:

- User with `case.create` can create a case from a template.
- Response displays case number, target user, reason, status, and queued actions.
- UI clearly explains that case history reload is not available yet.

### Slice 5: Discord Testing Aid

Build:

- A small "Test in Discord" panel for the selected guild.
- Show the command shape: `/case add template:<slug> user:<user> reason:<optional>`.
- Show whether the selected template is enabled and has at least one enabled action.
- Link this panel conceptually to the last created/edited template.

Acceptance criteria:

- A tester can create a template in the dashboard and understand how to try it with `/case add`.
- The dashboard does not need to call Discord directly for command testing.

## Known Backend Gaps

Do not build full UI for these yet unless the backend adds endpoints:

- Case list, detail, search, and timeline reads.
- Audit log listing.
- Staff management and staff disabling.
- Guild settings editing, including mod-log channel configuration.
- Permission policy editing.
- Bot invite/install flow endpoint.
- Appeals.
- Tickets.
- Rollout state management.
- Irreversible action execution for timeout, kick, and ban.

## Error Handling

Backend errors are usually:

```json
{
  "error": "message"
}
```

Expected statuses:

- `400`: invalid payload or validation failure.
- `401`: missing or expired auth session.
- `403`: authenticated but missing permission or disabled staff.
- `404`: template or guild-scoped resource not available.
- `502`: Discord API lookup failed while listing or resolving guilds.
- `500`: backend operation failed.

The dashboard should surface the `error` string when present.

## Suggested Frontend Data Model

Keep the client types close to the API contract:

- `SessionUser`
- `GuildListItem`
- `GuildContext`
- `PermissionMap`
- `CaseTemplate`
- `TemplateAction`
- `EscalationRule`
- `CreatedCase`
- `CreatedCaseAction`

Recommended state shape for the dashboard:

```ts
type DashboardState = {
  session: SessionUser | null
  guilds: GuildListItem[]
  selectedDiscordGuildID: string | null
  guildContext: GuildContext | null
  templates: CaseTemplate[]
  lastCreatedCase: CreatedCase | null
}
```

## First Website Agent Task

Start with the smallest useful vertical slice:

1. Build a single authenticated `/dashboard` workspace.
2. Wire `/auth/me`, `/auth/discord/login?mode=json`, `/auth/logout`, and `/status`.
3. Wire `GET /guilds`.
4. Let the user select an installed guild.
5. Wire `GET /guilds/:discordGuildID/me`.
6. Render modules as available/unavailable based on permission actions.

Do not start template forms until the auth and guild context path works reliably.
