# Architecture

Quack v5 is one Go binary, `quack`, built from `apps/backend`. `quack serve`
runs the Discord bot, the dashboard HTTP API, the action workers, and the
optional modules in one process, backed by MySQL and Redis. Product rules live
in [`v5.md`](../v5.md); this document describes how the code implements them.

## Packages

All packages live under `apps/backend/internal` unless noted.

| Package | Responsibility |
| --- | --- |
| `cmd/quack` | The binary: `serve`, `migrate`, and `import-v4`. |
| `config` | Loads settings from code defaults, an optional TOML file, and `QUACK_*` env vars, and validates them. |
| `quack` | The moderation domain: templates, escalation, cases, actions, notifications, appeals, audit, statistics, and the ports it needs. |
| `store` | GORM/MySQL and Redis implementation of the `quack` storage ports, the schema, and migrations. |
| `discord` | Discord adapter: REST client behind the `quack` Discord ports, interaction router, message and response model, `/case`, `/setup`, `/template`, `/appeals`, `/help`, the appeal form and queue, views, command sync and command mentions, guild lifecycle. |
| `discordtext` | Transport-free prose helpers for Discord copy: `{{quack:key}}` icon placeholders and the generated per-application emoji catalog, Markdown escaping, quoting, and the conversation layout. |
| `api` | The dashboard's `net/http` API: middleware, sessions, OAuth, and handlers over `quack.Services`. |
| `worker` | The in-process action queue, the database poller behind it, and periodic background loops. |
| `modules` | What the optional modules share: per-guild toggles and settings, the module actor, guild ID mapping, audit adapter, and a bounded work pool. |
| `modules/tickets` | Private support tickets: storage, Discord components, gateway handlers, HTTP routes, transcripts. |
| `modules/logging` | General logging of Discord events to staff-only channels. |
| `modules/honeypot` | Trap channels that open a case through the normal case path. |
| `v4import` | Parses and validates v4 case history exports for `quack import-v4`. |
| `app` | Composition root: builds every component from config, runs them, and stops them in reverse order. |
| `testutil` | Shared test stores (in-memory SQLite plus miniredis). |

Dependency direction:

```
cmd/quack ──> app ──> api, discord, worker, store, modules/*, config
                         │       │       │      │        │
                         └───────┴───────┴──────┴────────┴──> quack
```

- `quack` imports no other internal package and no infrastructure (no config,
  discordgo, GORM, Redis, or `net/http`). Storage and Discord are reached
  through small interfaces in `quack/ports.go` and `quack/discord.go`.
- `store` also imports `modules`, `modules/tickets`, and `modules/honeypot` to
  migrate their tables, and implements the `v4import.Repository` interface.
- The three modules import `discord` for the router, `/setup`, and the
  bot. Module HTTP routes are mounted through the `modules.Mux` interface,
  which `api.ModuleMux` satisfies, so modules do not import `api`.
- Only `app` and `cmd/quack` see the whole graph.

`quack.New` (`quack/quack.go`) builds `quack.Services`: `Guilds`, `Settings`,
`Templates`, `Cases`, `Actions`, `Evidence`, `Appeals`, `Audits`,
`Statistics`, `Ops`, and `Publications`. Every adapter calls these services; business rules
belong there, not in handlers.

## Startup and shutdown

`app.Run` (`app/app.go`) starts things in this order:

1. Open MySQL and Redis, then apply pending migrations (`store.Migrate`).
2. Create the Discord session (`discord.New`), requesting only the `Guilds`
   intent.
3. Wire everything together: the worker, the module registry,
   `quack.Services`, the three modules, the background loops (appeal
   notifications every 5s, audit mirror every 5s, case receipt refresh every
   2s, ticket transcript and journal sweep every hour, honeypot upkeep and
   honeypot warnings every second), gateway intents for enabled modules,
   guild lifecycle handlers, module gateway handlers, the interaction router
   with each module's components and `/setup` subcommand, and the HTTP server.
4. Sync slash commands (`discord.SyncCommands`). Command definitions are
   fingerprinted in Redis so unchanged commands are not rewritten. Sync also
   records each command's ID so copy like `/case view` renders as a clickable
   command mention. Only the `dev` environment registers `/ui-preview`, a
   gallery of message designs.
5. Start the worker and the logging and honeypot pools.
6. Open the Discord gateway.
7. Serve HTTP until the context is cancelled (SIGINT or SIGTERM).

On shutdown the HTTP server drains first. Then the remaining components stop
newest first (Discord gateway, honeypot pool, logging pool, worker, Redis,
MySQL). One `api.shutdown_timeout`, started when shutdown begins, bounds the
whole sequence. The worker stops polling and drains cases already queued.

Gateway intents are decided once at startup. Logging adds members, moderation,
messages, and message content; honeypot adds guild messages; tickets add
members, guild messages, and message content for the message journal. Each is
added only if at least one guild has that module on, so a module switched on
later gets its events after the next restart.

## How `/case add` flows

```
Discord ──interaction──> discord.Router ──> /case add handler
                                             │ resolve staff once (live REST)
                                             │ authorize case.create
                                             v
                                quack.CaseService.Create
                                 │ idempotency replay check
                                 │ preflight (outside lock): template, level,
                                 │   live target/bot/hierarchy check,
                                 │   context validation, evidence capture
                                 v
                        store.WithGuildCaseLock (SELECT ... FOR UPDATE on guild)
                                 │ re-check idempotency, re-select level,
                                 │ compare with preflight (retry if stale)
                                 │ one transaction: case, snapshot, event,
                                 │   executions, notification, evidence, audit
                                 v
                        Scheduler.Submit(caseID) ──> worker queue
                                                       │ (or the poller)
                                                       v
                                  quack.ActionService.ProcessCaseActions
                                   │ claim + lease each execution, enforce
                                   │ in Discord, record the attempt
                                   v
                                  send the one member notification
```

Step by step:

1. **Router** (`discord/router.go`). Each interaction ID is claimed in Redis
   (`discord:interaction:<id>`, 15 minutes) so a redelivered interaction never
   runs twice. The claim fails closed if Redis is down. Commands dispatch by
   name, components and modals by the namespace and action in their custom ID
   (`namespace:action:version:payload`).
   Handlers answer within Discord's three-second window and do slow work in a
   deferred task. Panics are recovered.
2. **Handler** (`discord/case_create.go`). The handler resolves the moderator's
   staff context once from live Discord state and passes it down. It defers
   and calls `CaseService.Create` with source `discord`, the interaction ID as
   the idempotency key, and the optional `message_link` and `file` as
   evidence. Optional context is added afterwards with Edit context. Only a
   template with required context fields opens a modal first; templates with
   more than five fields span several modal pages, with the draft kept in
   memory. The "Add case" message action and the "Add case for member" user
   action (`discord/case_create.go`) answer privately with a rule picker (25
   rules a page, skipped when there is one), then post the receipt to the
   channel with bot credentials and remove the picker. Sync deletes their old
   names ("Create moderation case", "Create case for member") once the new
   ones are registered.
3. **Preflight** (`quack/case.go`, `quack/authz.go`, `quack/evidence.go`). This
   is the slow work, done before taking the lock. It loads the template,
   selects the level, re-checks Discord, validates context values, and captures
   linked messages. The Discord check confirms the actor and bot are present,
   the target is a current human member who is not the actor, a bot, Quack, or
   the owner, the target is below both the actor and the bot in the role
   hierarchy, and both hold the permission the level's action needs. Evidence
   capture snapshots the message and copies attachments into the guild's
   managed evidence channel. A link that can't be captured is only accepted
   when the moderator gave other visible context.
4. **Locked write** (`store/store.go`, `store/cases.go`). Under a row lock on
   the guild, the service re-checks the idempotency key, re-selects the level,
   and compares it with the preflight. If another case for the same member
   landed in between, the preflight is stale and creation retries, up to four
   attempts. `store.CreateCase` assigns the next case number for the guild and
   writes the case, its template snapshot, a `created` event, one pending
   execution per level action, a notification row if the level notifies, the
   evidence, and the audit entries in one transaction.
5. **Scheduling**. After commit, the case ID is submitted to the worker queue.
   Submission is only a latency shortcut: if the queue is full or stopped, the
   poller finds the case anyway.
6. **Enforcement and notification**. See the action engine below.
7. **Public receipt** (`discord/case_receipt.go`). The command defers
   publicly (`discord.AsyncPublic`) and the receipt replaces that placeholder
   in place: case number, target, rule, reason, action progress, moderator,
   and quoted context, never evidence. Its buttons (Edit context, View
   evidence, History, Void case) recheck the moderator's authority on every
   click. The receipt is recorded as a case publication (see Case
   publications), so it keeps up with enforcement, voids, and context edits
   long after the interaction expires. An error deletes the placeholder and
   goes to the moderator in an ephemeral followup.


Dashboard case creation (`POST /guilds/{discordGuildID}/cases`) calls the same
`CaseService.Create` with source `dashboard` and the request's
`Idempotency-Key`. Honeypot cases use `CaseService.CreateSystemHoneypot`, which
skips the staff checks but keeps every target and bot check.

### Messages

Every Discord message is plain text in one layout, `discordtext.Conversation`:
an icon and a lead sentence, an optional quote, details, and a `-#` subtext
line. Views write `{{quack:key}}` icon placeholders and command references
such as `/case view`; the router, the interaction responder, and `Bot.Send`
resolve both for the sending application just before Discord sees them
(`Message.ForApplication`). The emoji IDs come from
`discordtext/icons_generated.go`, generated from
`assets/icons/quack/manifest.json` by `assets/icons/quack/generate-catalog.mjs`
(`go generate ./internal/discordtext`). An application without uploads gets
the same text without icons. Content too long for one message keeps its
leading paragraphs and attaches the rest as `message.txt`. Mentions are
suppressed unless a message allows them, link previews are hidden, and
ephemeral flags are dropped in DMs, where Discord rejects them.

## Escalation

`CaseService` picks the level in `quack/escalation.go`:

- The count is the number of prior cases for the same guild, member, and
  template ID that are still valid and did not come from the v4 import, plus
  one for the case being created (`store.CountTemplateCasesForTarget`).
  Template edits keep the template ID, so every version counts.
- Counts are all-time unless the template sets `case_decay_days` (1 to
  36500). Then only cases created in that many days before now count
  (`CountTemplateCasesForTargetParams.CreatedAtOrAfter`). Older cases stay
  valid and visible; they just stop counting.
- The level with the highest `trigger_case_count` the count has reached wins.
  If none has been reached, the template's default level applies. Each
  template has exactly one default level (enforced by a unique index; see
  [`migrations.md`](migrations.md)), and each level has at most one action.
- The case stores a snapshot (`cases.template_snapshot_json`) of the template
  version and its decay window, the selected level with the count that
  matched, the action settings, the context fields, and the submitted values. Later template edits
  never change an existing case; appeal eligibility and notifications read the
  snapshot.

Voiding a case (`CaseService.Void`) keeps the row, marks it voided with a
reason, and removes it from future counts. Corrections are void plus a new case
with `replaces_case_id`. Voiding cancels executions that have not started and
fails the notification if it has not been sent. Work that is already running
or sending is left alone.

In the same transaction, voiding queues a reversal of each succeeded timeout
and ban (`store.queueVoidedCaseReversals`), with the voider recorded as
`requested_by` in its configuration and, when an accepted appeal voided the
case, the appeal as `reversal_appeal_id`. A punishment that succeeds after its
case was voided is reversed the same way, and one that would retry fails for
review instead. Before an automatic reversal runs, the worker checks that the
member has no other timeout or ban that it would also lift
(`store.CompetingPunishmentExists`) and that the voider still passes
`GuildService.PreflightReversal`; otherwise it fails for staff review.

Staff can also update a case's context (`CaseService.UpdateContext`, free text
that replaces the context values, with any new Discord message links in it
captured as evidence) and add evidence (`CaseService.AddEvidence`, message
links and uploaded files). Neither touches the decision or its enforcement.

When Quack joins a guild, the `GuildCreate` handler (`discord/lifecycle.go`)
calls `GuildService.BootstrapDiscordGuild`, which creates or reactivates the
guild, its settings row, and the starter template (`quack/starter.go`): a
notifying default level, a 24-hour timeout at three cases, and a ban that
deletes 24 hours of messages at five. A one-time dashboard notice asks admins
to review it. The handler then makes sure the managed evidence channel exists:
a missing one is created hidden from everyone but Quack and staff roles, and
an existing one is left as administrators set it up. Saved copies are linked
by their message in that channel, which outlives Discord's signed file URLs.
Leaving a guild only marks it inactive; its history stays.

## Action engine

Each enforcement is a row in `case_action_executions`, and each try is a row in
`case_action_attempts`. Action types are `timeout_user`, `kick_user`, and
`ban_user`, plus the reversals `remove_timeout` and `unban_user`.

```
pending ──claim──> running ──ok──────────────> succeeded
   ^                  │
   │                  ├─error, safe to retry──> retrying ──(backoff)──> running
   │                  │
   │                  └─error, otherwise, or ─> failed ──staff dismiss──> (dismissed, kept)
   │                     lease expired             │
   └───────────────staff retry─────────────────────┘
pending/retrying ──case voided──> cancelled
```

- **Leases.** `store.ClaimNextCaseAction` takes the case's next due execution
  in position order, marks it running with a fresh lease token for two minutes,
  and opens an attempt. It claims nothing while another worker holds a live
  lease on the same case. Completion is fenced by the lease token.
- **Expired leases.** If a lease expires while running, Discord may already
  have applied the action. The execution fails with `lease_expired` for staff
  review and is never retried automatically.
- **Automatic retry.** After a failure (`quack/action.go`), Quack
  allows another attempt only when the error is classified retryable, the
  outcome is certain, the execution is `safe_for_retry`, and the attempt count
  is still within `max_retries`. Errors the Discord adapter did not classify
  count as uncertain. Template actions are safe to retry with a one-second
  backoff; reversals are not.
- **Attempts.** Each Discord call has a 30-second timeout. The attempt's
  request, redacted response, and error code are stored with a case event and
  an audit entry.
- **Staff controls.** These are available from the dashboard
  (`/guilds/{discordGuildID}/action-failures/...`) and from `/case failures`,
  `/case retry`, `/case dismiss`, and `/case reverse`.
  - Retry re-runs the live preflight, then requeues a failed execution.
  - Dismiss takes it out of the review queue and keeps its history.
  - Reverse queues `remove_timeout` or `unban_user` against a succeeded
    timeout or ban, after `GuildService.PreflightReversal`. An unban target may
    have left the guild. Kicks cannot be reversed.
  - Every control is audited, including denials.

### Notifications

A case has at most one member notification (`case_notifications`), created
with the case when the selected level has `notify_user` set.

- Before a kick or ban, the worker opens the member's DM channel while they
  still share a guild (`pending` to `prepared`).
- Once no execution is pending, running, or retrying, the notification is
  claimed with a lease (`claimed`), moved to `sending` just before the DM goes
  out, and finished as `sent` or `failed`.
- A notification in `sending` is never reclaimed, so a crash mid-send cannot
  produce a second DM. Failures are recorded on the case and not retried.
- The message (`quack/notify.go`) has the guild name, official reason, context
  values, outcome, case number, and the guild's optional introduction and
  footer, capped at 2000 characters. A messenger that implements
  `quack.CaseNotificationSender` gets a `CaseNotificationRequest` instead (rule
  name, outcomes with the confirmed timeout end, appeal URL) and renders the
  DM itself. The Discord bot does (`discord/notify.go`): an appealable case's
  DM carries an "Appeal decision" button (`appeal:submit:v1:<case ID>`).
- A messenger without that interface sends the plain wording, and if the
  case is appealable and an `https` dashboard origin is configured (the first
  `https` entry in `api.cors_origins`), an "Open appeal" link to
  `<dashboard>/guilds/<guild>/cases/<case>/appeal`.

### Polling

The worker (`worker/worker.go`) is a bounded channel (`queue.size`) drained by
`queue.workers` goroutines. A case already waiting is not queued twice. Every
second the poller asks `store.ListExecutableCaseIDs` for up to 100 cases with:

- executions that are pending or retrying and due, or running with an expired
  lease, or
- notifications that are pending, prepared, or claimed with an expired lease,
  on cases with no active executions.

Results are interleaved across guilds so one busy guild cannot starve the
rest. The database is the source of truth; the queue only saves latency.

## Appeals

`quack.AppealService` (`quack/appeal*.go`) owns appeals.

- **Who can appeal.** The signed-in member must be the case's target. Guild
  membership is not required, so a banned member can still appeal. The case
  must be valid, its snapshot must say the template was appealable, and it
  must not already have an appeal. A case gets at most one appeal.
- **Form.** Guilds can set up to ten `short_text`, `long_text`, or `boolean`
  questions (`guild_appeal_settings`); otherwise a two-question default
  applies. The form and answers are snapshotted on the appeal.
- **Statuses.** `pending`, `needs_information`, `accepted`, `rejected`,
  `closed`. Staff can request information (pending to needs_information), and
  the member's reply returns it to pending. Staff can accept or reject a
  pending appeal, close a pending or needs_information one, and reopen a
  rejected or closed one (to needs_information).
- **Accepting** records the decision and voids the case in the same
  transaction, which queues reversals of its timeouts and bans linked to the
  appeal (see Escalation). The staff view still offers a reversal for any
  succeeded timeout or ban that has none, through
  `POST /guilds/{discordGuildID}/appeals/{appealID}/reversals`, which runs
  `ActionService.ReverseForAppeal` with the full live preflight.
- **Settings.** `guild_settings` holds the appeal queue channel (separate
  from the audit mirror), an optional rejoin invite sent with accepted
  appeals, and whether staff must write a decision reason
  (`AppealService.ReviewReasonRequired`). `AppealService.CanSubmit` checks
  eligibility before a Discord appeal form opens.
- **Notifications.** Every appeal change writes a row to
  `appeal_notifications` in the same transaction. A worker loop
  (`quack.AppealNotificationDispatcher`) leases and sends up to 50 every five
  seconds. A row moves to `sending` just before it goes out and is never
  reclaimed from there, so a crash cannot send twice; deliveries that reached
  nothing (`delivery_deferred`) are retried after a minute. Member decisions
  carry a frozen `AppealDecisionIntent` (status, reason, case number, guild
  name, rejoin URL). Staff rows are one queue post per appeal: a notifier that
  implements `quack.AppealQueuePublisher` posts the appeal once and later
  rows edit it in place, using the receipt (`delivery_channel_id`,
  `delivery_message_id`) and `refresh_requested`. Notifiers without the rich
  interfaces get plain bodies. Messages never name the staff member.
- **In Discord** (`discord/appeal_*.go`, `discord/appeals.go`).
  `discord.AppealNotifier` implements both rich interfaces. The "Appeal
  decision" button (`appeal:submit:v1:<case>`) checks `CanSubmit` and opens
  a one-question form whose answer is saved as the `reason` answer, so it
  fits the default form. The queue post in the appeal queue channel shows
  the statement in pages, Accept and Reject while pending (with a reason
  form when the guild requires one), and "Confirm ..." buttons for
  reversals still on offer after acceptance. `/appeals` pages through
  pending appeals straight from storage. Every staff control re-reads live
  permissions. The decision DM quotes the reason and carries a Rejoin
  Server button when an accepted appeal has an invite.

## Audit log and mirror

- `audit_log_entries` is append-only. `store.New` installs GORM callbacks that
  refuse updates and deletes on that table. Metadata and failure text are
  redacted on write (`quack.RedactAuditMetadata`).
- Moderation changes write their audit entry in the same transaction as the
  change. Permission-sensitive reads and denials are audited too.
- Staff read the log through `GET /guilds/{discordGuildID}/audit-log` with
  filters and a `before_id` cursor. `GET .../statistics` is computed from
  cases, executions, appeals, and audit rows; there is no separate statistics
  table.
- `quack.AuditMirror` (`quack/audit_mirror.go`) polls every five seconds for
  important entries (`quack.ImportantAuditActions`) that have no successful
  mirror outcome yet and posts them to the guild's audit mirror channel.
  Entries about a case, one of its executions, or its appeal carry the case
  number, target, rule name, the selected level and outcome (on
  `case.create`), whether a reversal found the punishment already over, and
  the execution staff can still retry. The Discord entry
  (`discord/views_audit.go`) is one line with those details in subtext
  beneath it, and a retryable failure gets a "Retry action" button that runs
  the same checks as `/case retry`. Each outcome (delivered, skipped, failed) is itself an audit entry, which is how
  the mirror knows an entry is done. Failures retry after a minute. If the
  channel is gone, the mirror clears the setting and stops trying it.
- The audit mirror is separate from the general logging module.

## Case publications

Public Discord messages about a case, such as the `/case add` receipt, are
recorded in `case_publications` (`CasePublicationService.Record`) and kept up
to date with bot credentials, so they outlive the interaction token. Every
transaction that changes what a receipt shows (claiming or completing an
execution, retrying, reversing, voiding, updating context or evidence) sets
`refresh_requested`, clears `last_digest`, and bumps `revision`
(`store.requestPublicationRefresh`). A refresh loop
(`discord.PublicationRefresher`, run by the worker every two seconds) asks
for due publications (`Due`), renders each from its stored presentation and
`CaseReceipt`, skips the edit when the digest is unchanged, and reports back
(`Complete`), or retires the publication when the message or case is gone
(`Retire`). A receipt still settling is checked again in two seconds, a
failed refresh in thirty, and a settled one waits for the next change.
Completion is fenced on `revision`, so a change committed during a refresh is
never lost.

## HTTP API

`api.Server` (`api/api.go`, routes in `api/routes.go`) uses stdlib
`net/http` with Go 1.22 `ServeMux` patterns. The JSON contract is fixed by the
dashboard and described in `contracts/http/swagger.yaml`.

Every request passes through one global chain:

1. Trace IDs: adopts or generates `X-Request-ID` and
   `X-Correlation-ID` and echoes them.
2. Observation: buffers the response, recovers panics as 500s, logs one line
   with the route pattern (never the raw path or query), and rewrites any error
   body into the standard envelope:
   `{"error": {"code", "message", "request_id", "correlation_id"}}`.
3. Security headers: a deny-all CSP, `nosniff`, `X-Frame-Options: DENY`,
   `no-referrer`, and `no-store`.
4. CORS: allows exactly the `api.cors_origins` with credentials and rejects
   any other `Origin` with 403.
5. Body limit: caps bodies at `api.max_body_bytes`.
6. CSRF: a cookie-authenticated write must come from an allowed `Origin` and
   echo the CSRF cookie in `X-CSRF-Token` (double submit). Bearer-token
   callers skip it.

Unknown paths and wrong methods both return 404.

Guild staff routes then add, in order:

- **Endpoint policy.** Pagination bounds, then a rate limit keyed by
  credential and guild. Reads use `limits.member_read`, writes
  `limits.template_write`, case creation `limits.case_create` plus
  `limits.evidence`, and retries and reversals `limits.retry`.
- **Session.** The session comes from the session cookie or a bearer
  token. Sessions live in Redis with a sliding expiry, and an expired session
  or Discord grant answers `reauthentication_required`.
- **Guild context.** `GuildService.ResolveStaffContext` reads the caller's
  live Discord permissions, and `Authorize` checks the route's capability.
- **Idempotency, for writes.** `Idempotency-Key` is required. The key holds a
  fenced Redis lease while the write runs, then the stored response, which is
  replayed for `api.idempotency_ttl`. Reusing a key with a different body is a
  409.

Member routes (`/members/me/...`) use the signed-in user instead of a guild
context. Appeal staff routes check finer capabilities in the service, and
appeal writes check the capability before idempotency so a demoted reviewer
cannot replay. Rate limits and idempotency fail closed with 503 when Redis is
down.

Capabilities come from Discord permissions (`quack/guild.go`):

- Owner and `Administrator` can do everything.
- `Manage Guild` can manage templates and settings.
- `Moderate Members` can create, read, and void cases, review appeals, read
  the audit log, and work the ticket queue.

OAuth (`api/oauth.go`): `GET /auth/discord/login` stores a single-use state in
Redis bound to a browser cookie. `GET /auth/discord/callback` exchanges the
code and creates the session. Tokens stay server-side in Redis.

## Optional modules

The three modules each own their domain, storage, Discord glue, and HTTP
routes, and plug into the core in the same ways:

- **Toggles and settings.** `modules.Registry` reads and writes
  `module_configurations`, one row per guild and module with an `enabled` flag
  and opaque JSON settings. It also implements `quack.ModuleToggles`, so the
  core settings API's `tickets_enabled`, `general_logging_enabled`, and
  `honeypot_enabled` fields read and write the same rows. Before the settings
  service switches a module on, it asks the registry
  (`quack.ModuleEnablementChecker`): a module never set up is refused with
  "run /setup first", and otherwise the module's `EnablementCheck` re-runs
  its `/setup` checks against live Discord (channels exist and are usable,
  Quack has the permissions it needs) without writing anything.
- **Setup.** `/setup` (`discord/setup.go`) needs Manage Server, checked live,
  and answers publicly through `AsyncPublic`. The `appeals` and `audit`
  subcommands are core settings: the appeal queue channel, rejoin invite, and
  whether decisions need a reason, and the audit mirror channel. `tickets`,
  `honeypot`, and `logging` go to the handler each module installs with
  `Router.HandleSetup`; with only the `enabled` option they switch the module
  through the settings service instead, which keeps its saved setup. When no
  channel is given, `discord.SetupChannel` reuses the configured one or
  creates a channel with permissions, a topic, and, for staff channels, a
  short introduction. It replaces a configured channel only when Discord
  says it is gone, so a transient error never creates duplicates.
- **HTTP.** Each module's `MountHTTP` adds routes under
  `/guilds/{discordGuildID}/modules/` through `api.ModuleMux`. These routes get
  the endpoint policy, session, live guild context, and a per-actor limit, and
  writes require an `Idempotency-Key`.
- **Discord.** `RegisterGateway` subscribes to gateway events. Tickets also
  registers components on the router.
- **Background work.** Gateway work runs in a bounded `modules.Pool` that drops
  jobs when full, so module traffic never delays moderation.
- **Audit.** Module events go to the core audit log through
  `modules.AuditLog`.

What each module does:

- **Tickets** (`modules/tickets`). `/setup tickets` saves an entry channel
  and a staff queue channel and posts the "Need a hand?" entry panel with an
  Open ticket button, editing it in place (or moving it) on every re-setup.
  Opening creates a private thread under the entry channel with a welcome and
  a Close button, and a queue post with staff controls. A message journal
  (`ticket_message_journal`) records each message's original text from the
  gateway; closing refuses while the journal is incomplete, then locks the
  thread, merges the journal with the surviving history into a transcript,
  edits the queue post to closed with the transcript as a `.txt`, DMs the
  member a copy, and deletes the thread. Staff can repair a queue post that
  failed or went missing (retry, adopt an existing message, or confirm it was
  never sent). Closed tickets cannot be reopened. An hourly loop deletes
  transcripts and journaled text past retention. Tables: `tickets`,
  `ticket_events`, `ticket_transcripts`, `ticket_member_states`,
  `ticket_message_journal`. Routes live under `/modules/tickets/...`.
- **General logging** (`modules/logging`). Posts message edits and deletes,
  joins and leaves, bans, and guild and channel changes to the staff-only
  channels a guild picks, as readable Quack messages (for example "A message
  from @x was edited." with Before and After). `/setup logging` routes every
  event to one channel and needs View Audit Log, which attributes bans by
  other moderators. Recent messages are cached in memory only, to show
  what changed. No tables of its own. Routes live under
  `/modules/general-logging/...`.
- **Honeypot** (`modules/honeypot`). When someone posts in the trap channel,
  it opens a case with the configured template through
  `CaseService.CreateSystemHoneypot`, then deletes the bait message through a
  durable cleanup queue (`honeypot_message_cleanups`). Staff, Quack, and
  webhooks are ignored (other bots are not), a burst from one member is one
  incident, and `honeypot_triggers` makes each message
  fire at most once; the upkeep loop finishes incidents a restart
  interrupted without ever punishing twice. `/setup honeypot` creates the
  template with `TemplateService.EnsureHoneypotTemplate` when there is none
  and posts a warning worded from the template's punishment with a live
  "N incidents caught." counter; `honeypot_warning_refreshes` schedules its
  edits and reposts it if it is deleted. The API notifies the module when a
  template is updated or archived so it can recheck its configuration.
  Routes live under `/modules/honeypot/...`.

## Storage

- `store.Store` implements every `quack` storage port over GORM/MySQL and
  Redis. Each moderation change commits in one transaction with its timeline
  events and audit entries.
- IDs are ULIDs. One record struct per table lives in `store/schema.go`.
  Migrations are covered in [`migrations.md`](migrations.md).
- Redis holds dashboard sessions and OAuth state (`auth:*`), interaction
  dedupe claims, the command sync cache, rate limit counters, and idempotency
  records. No moderation state lives only in Redis.
- `store.New` accepts a nil Redis client for tools that only need MySQL
  (`migrate`, `import-v4`).

## Known gaps

These are verified against the code as of this writing:

- **The Discord appeal form asks one question.** Guilds with a custom
  appeal form whose questions do not include `reason` must take appeals
  through the dashboard.
- **No real-guild rehearsal yet.** Install, permissions, enforcement, DMs,
  appeals, and the modules have been tested against fakes, SQLite, and MySQL,
  but not end to end in a live Discord guild.
- **One process only.** Multi-page `/case add` drafts are held in memory, and
  every process connected to the gateway receives every event, so running
  more than one `serve` process would split drafts and duplicate general
  logging posts.
