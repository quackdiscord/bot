# Quack v5 design

Quack v5 is a customizable moderation system for Discord servers. This
document is the single source of truth for what Quack does and how the code
does it. If code and this document disagree, one of them is wrong: fix the
code, or change this document first when the change is intentional.

Its central rule:

> Admins define the server's moderation rules. Moderators apply those rules.
> Quack chooses and carries out the configured result.

## Contents

1. [System at a glance](#system-at-a-glance)
2. [Product](#product)
3. [Architecture](#architecture)
4. [How it works](#how-it-works)
5. [Dashboard](#dashboard)
6. [Configuration](#configuration)
7. [Development](#development)
8. [Testing](#testing)
9. [Migrations](#migrations)
10. [Operations](#operations)
11. [v4 import](#v4-import)
12. [Known gaps](#known-gaps)

## System at a glance

```
browser ──> quack-dashboard (:3000) ──/api/*──> quack serve (:8080) ──> MySQL 8
                 │                                  │      │       └──> Redis
                 └── the built React app            │      └── Discord REST + gateway
                                                    └── action workers, background loops, modules
```

- `apps/backend` is one Go module that builds one binary, `quack`.
  `quack serve` runs the Discord bot, the dashboard HTTP API, the action
  workers, and the optional modules in one process.
- `apps/dashboard` is a React single-page app plus a small, stateless Go
  server (`quack-dashboard`) that serves it and proxies `/api` to the backend.
- MySQL holds all durable state. Redis holds sessions, dedupe claims, rate
  limits, and idempotency records; no moderation state lives only in Redis.
- `contracts/http/openapi.yaml` is the HTTP contract, generated from the
  backend's route table and consumed by the dashboard's typed client.

## Product

### Building blocks

- A **template** is a moderation rule for one kind of problem, such as spam
  or harassment. The Discord and dashboard UI call templates **rules**.
- A **level** says what happens as the same member receives more cases from
  that template.
- A **case** records that a template was applied to a member.
- An **action** is the Discord enforcement a level chooses: timeout, kick, or
  ban.
- An **appeal** asks staff to review one case.
- The **audit log** is the permanent record of important activity.

A moderator picks the template and the member and supplies the requested
context. Quack checks the member's history, chooses the level, creates the
case, performs the action, notifies the member, and records everything.

Compared with v4, moderators no longer choose `/warn`, `/timeout`, `/kick`, or
`/ban` and decide the punishment themselves; those direct commands are gone.
Admins define consistent flows ahead of time, and Discord and the dashboard
share the same behavior and records.

### Principles

- **The guild owns its rules.** Admins control templates, levels, reasons,
  actions, appealability, and requested context. Quack ships a starter rule
  that admins can edit, replace, export, import, or archive.
- **Moderators apply rules; they do not invent them.** They never choose the
  level, rewrite the official reason, or replace the configured action.
- **Discord is the authority for access.** Discord owns identity, membership,
  roles, permissions, and hierarchy. Quack has no staff-role system of its own.
- **History stays understandable.** A case keeps a snapshot of the template
  version, level, reason, context, evidence, action, and result it used. Later
  template edits never rewrite old cases.
- **Guild data stays inside the guild.** History in one guild never affects
  another.
- **Important records are not hard-deleted.** Incorrect cases are voided and
  stay visible. Audit entries are never edited or deleted.

### People and permissions

Quack re-reads current Discord permissions before every sensitive operation.
Cached data may feed displays but never grants lasting access; losing a role
takes effect on the next request, and earlier actions stay attributed.

| Who | Can |
| --- | --- |
| Guild owner, `Administrator` | Everything. |
| `Manage Guild` | Create, edit, import, export, and archive templates; configure the audit mirror, appeals, and modules; manage guild settings. |
| `Moderate Members` | Apply templates and create cases; review cases and member history; read the full audit log; review appeals; void cases with a reason; dismiss failed actions (retry also needs the action's permission); work the ticket queue. |
| The case's target | See their own valid and voided cases and appeal them, even after leaving or being banned. Nothing else in the guild. |

Creating a case grants no extra Discord power. When the selected level has an
action, the moderator also needs the matching permission (timeout:
`Moderate Members`, kick: `Kick Members`, ban: `Ban Members`; a retry needs
the original action's permission), and both the moderator and the bot must
be above the target in the role hierarchy. If the moderator cannot perform
the level Quack selects, the whole request is refused before a case exists.
Quack never creates a case with its action silently dropped.

### Surfaces

Discord and the dashboard are two ways into the same services. Neither has its
own moderation logic.

- **Dashboard.** Admins build templates and configure the guild. Moderators
  apply templates, add detailed context, search cases, work failures, and
  review appeals. Members sign in with Discord to see their cases and submit
  and track appeals.
- **Discord.** Moderators apply templates while working in the server, review
  cases and history, capture evidence from live messages with message
  actions, and work failures and appeals. Server managers can create and
  maintain rules with `/template` (name, reason, appealability, decay, and
  per-count outcomes) and configure appeals, the audit mirror, and modules
  with `/setup`. Context-field design and template import and export are
  dashboard-only. Case DMs lead members into the appeal flow.
- **HTTP API.** It serves the dashboard and Quack's own adapters. It is not a
  public integration or automation API.

### Templates

A template names an infraction ("Spam", "Harassment"), not a punishment.
Templates named "ban" or "timeout" recreate the v4 workflow and are not the
intended model. A template defines:

- A name, short description, and a fixed official reason shown to the member.
- Whether its cases can be appealed.
- The context fields a moderator completes.
- Exactly one default level, plus optional higher levels keyed by case count.
- For each level, whether it notifies the member and zero or one action.
- An optional decay window in days.

**Versions.** Editing a template creates a new version of the same template
identity. Earlier non-voided cases from any version still count toward
escalation, and every case keeps a snapshot of the version it used.

**Availability.** Archive is the only control. A saved valid template is
active immediately and can create cases; an archived one cannot, but it and
its cases stay visible, and it can be restored. There is no enabled switch,
draft state, or approval workflow.

**Import and export.** Exports contain rules only: never guild or channel IDs,
cases, member history, audit entries, secrets, or module settings. An import
becomes a new template owned by the receiving guild and is active once the
admin confirms it.

### Escalation

The count for a new case includes cases in the same guild, for the same
member, from the same template identity across versions, that are not voided,
not imported from v4, and inside the decay window if the template has one,
plus the case being created. Counts are all-time by default; with a decay
window, older cases stop counting but stay in history, and the window can be
changed without touching any case.

Quack selects the level with the highest threshold the count has reached;
otherwise the default level applies. A level keeps applying until a higher
threshold is reached. Escalation never uses severity labels, weights or
points, other templates, other guilds, or a moderator's choice.

### Starter policy

When Quack joins a guild it creates one active, editable, appealable
**General rule violation** template:

| Case count | Result |
| --- | --- |
| 1–2 | Create the case and notify the member. |
| 3–4 | Also apply a 24-hour timeout. |
| 5 and later | Also ban, deleting the previous 24 hours of messages. |

A one-time dashboard notice asks admins to review it. It is a starting point,
not a claim about how every guild should moderate.

### Cases

A case is itself a moderation outcome (a warning); an action is an optional
extra. Each case gets a human-friendly number unique within its guild and
never reused.

- **Validity.** A case is *valid* (counts toward escalation) or *voided*
  (visible, does not count). Action and appeal progress are separate; a
  failed action does not void the case.
- **Corrections.** Target, template, level, and action are permanent. A wrong
  case is voided with a reason, and a replacement case is created if needed,
  so both the mistake and the correction are on record. Staff may edit the
  context and add evidence; neither changes the decision.
- **Member view.** Members see guild, case reference, template and reason,
  context, evidence, outcome, appeal availability and status, and public
  history, whether or not the DM arrived. Member views and DMs never show
  moderator identities, internal details, worker information, or raw Discord
  errors. The public Discord receipt in the staff channel does show the
  moderator and context; staff and audit views keep exact actor identity.

### Context and evidence

Admins define context fields per template, each required or optional: short
text, long text, boolean, number, or Discord message link. All context is
visible to the affected member; there are no private case notes.

Evidence enters through a message context action on a live message or a
pasted message link (Discord or dashboard), and both use one capture flow.
Quack snapshots the text, author, guild/channel/message IDs, timestamps,
embed metadata, and attachment names, types, sizes, and URLs. The snapshot
belongs to the case and survives later edits or deletion. `/case evidence`
adds a file or message link to an existing case; given only the case, it
shows the evidence the case already has.

Quack manages a staff-only evidence channel per guild and copies supported
attachments into it. When Discord limits prevent a copy, the metadata and
original URL are kept, staff are warned, and the case still proceeds. A link
that is already deleted or unreadable is accepted only when the moderator
gives other visible context. No external object storage is used, and content
deleted before capture cannot be recovered.

### Actions and recovery

Each level has zero or one action: timeout (admin-set duration), kick, or ban
(optional message-history deletion). Admins may set the maximum number of
safe automatic retries. Moderators cannot change action settings while
applying a template; Quack owns timing, duplicate protection, timeouts, and
error classification.

Normal case creation targets current human members. Quack refuses to act on
the moderator, any bot, Quack itself, the guild owner, or anyone at or above
the moderator or the bot in the hierarchy. Imported history and special flows
may refer to departed users.

Every attempt and result is visible on the case and in the audit log. Quack
retries automatically only when it knows a retry is safe; uncertain failures
wait for staff, who can **retry** (re-checks permission and hierarchy),
**dismiss** (leaves the review queue, history kept), or **void** the case. A
different punishment needs a new case.

### Member notifications

A case sends at most one DM, and only if its level says so. When needed, the
DM channel is opened before a kick or ban, and the message goes out after the
action result is known. It contains the guild name, official reason, text,
boolean, and number context (message links and evidence stay out), the
outcome and whether enforcement succeeded, the case reference, appeal access
when allowed, and an optional guild introduction and footer. A failed DM does
not hide or invalidate the case; staff see the failure and the member can
still sign in to the dashboard.

### Discord case responses

A case created from Discord posts a public receipt in the channel where it
was invoked. Validation and permission errors stay private to the moderator.
Evidence is never copied into the channel.

### Appeals

Appeals are core, not a module. A template decides whether its cases are
appealable. Every appeal uses one fixed question (why the case should be
reconsidered); guilds cannot customize it. Each case has at most one appeal;
staff can reopen it to ask for more information, but members cannot file
repeats. Members enter from the case DM and submit and track the appeal in
the dashboard. Members with `Moderate Members` or higher review appeals.

Accepting an appeal records the decision, voids the case, and queues removal
of any timeout or ban it applied. The removal runs only while the reviewer
still has the permission and hierarchy, and only if no other punishment of
the same kind would be lifted with it; otherwise it waits for staff. Voiding
a case any other way reverses its timeout or ban the same way.

### Audit log and statistics

The audit log is Quack's permanent moderation and administration history,
separate from general Discord logging. It records template and setting
changes (successful and failed), denied permission-sensitive requests, case
creation and voiding, action attempts, results, retries, and dismissals,
appeal lifecycle events, module configuration changes, and system actions.
Successful reads are not recorded. Entries are append-only. All moderators
can read the full guild log in the dashboard, and a guild may mirror
important events to a staff-only Discord channel.

Staff statistics are computed from cases, actions, and audit history. They are
operational information, not leaderboards or performance judgments.

### Optional modules

Modules share Quack's backend and permissions but are enabled and configured
per guild, and must not complicate the case model.

- **Tickets.** Private support threads between members and staff. Ticket
  conversations, status, and transcripts are separate from cases and appeals.
- **General logging.** Sends configured Discord events (message and member
  changes and so on) to staff-only log channels. It is not a searchable
  archive and not the audit log.
- **Honeypot.** Activity in an admin-configured trap channel automatically
  applies one chosen template through the normal escalation, action,
  notification, evidence, and audit flow. It is the one exception to staff
  applying templates by hand.

Utilities such as purge, lockdown, or server info may return later; they do
not shape the core.

### v4 history

Imported v4 cases are labeled historical records: visible in history and
statistics, never counted toward escalation. Core migration imports cases
only and invents no mappings for tickets, logs, honeypots, or other legacy
data. v4 and v5 may run side by side without sharing state during a
transition; afterwards v4's direct moderation commands are removed.

### Firm boundaries

Not part of v5: cross-guild history; public third-party automation APIs;
moderator-selected levels; direct punishment commands as an equal workflow;
reason overrides; multiple actions per level; severity, weight, or points
escalation; cross-template escalation; private free-form notes; hard deletion
of cases or audit history; a Quack staff-role system; full template
configuration in Discord; general Discord logging inside the audit model.
Changing one of these boundaries starts with changing this document.

## Architecture

### Independent pieces

Each backend package is a self-contained piece with one job, and the pieces
fit together through small interfaces:

- `internal/quack` is the moderation domain. It owns every business rule and
  imports no other internal package and no infrastructure: no config,
  discordgo, GORM, Redis, or `net/http`. It reaches storage and Discord only
  through the ports it declares (`quack/ports.go`, `quack/discord.go`).
- `store`, `discord`, `api`, `worker`, and `modules/*` are adapters. Each
  implements or drives `quack` interfaces and can be replaced or faked without
  touching the domain. Adapters stay thin: business rules belong in
  `quack.Services`, not handlers.
- `internal/app` is the only composition root. It builds every piece from
  config, connects them, runs them, and stops them. Only `app` and
  `cmd/quack` see the whole graph.

```
cmd/quack ──> app ──> api, discord, worker, store, modules/*, config
                         │       │       │      │        │
                         └───────┴───────┴──────┴────────┴──> quack
```

A few edges exist beyond that picture, all one-way: `store` imports
`modules`, `modules/tickets`, and `modules/honeypot` to migrate their tables
and implements `v4import.Repository`; the modules import `discord` for the
router, `/setup`, and the bot; module HTTP routes mount through the
`modules.Mux` interface (satisfied by `api.ModuleMux`), so modules never
import `api`, while `api` imports `modules` for `modules.Doc` and
`modules.DecodeQuery`.

`quack.New` (`quack/quack.go`) builds `quack.Services`: `Guilds`, `Settings`,
`Templates`, `Cases`, `Actions`, `Evidence`, `Appeals`, `Audits`,
`Statistics`, `Ops`, and `Publications`. Every adapter calls these.

### Packages

Under `apps/backend` (`internal/` unless noted):

| Package | Responsibility |
| --- | --- |
| `cmd/quack` | The binary: `serve` (default), `migrate`, `import-v4`, `help`. |
| `cmd/openapi` | Writes `contracts/http/openapi.yaml`; run by `go generate ./...`. |
| `config` | Loads defaults, an optional TOML file, and `QUACK_*` variables; validates. |
| `quack` | The domain: templates, escalation, cases, actions, notifications, appeals, audit, statistics, publications, and the ports they need. |
| `store` | GORM/MySQL and Redis implementation of the storage ports, the schema, and migrations. |
| `discord` | REST client behind the Discord ports, interaction router, message model, `/case`, `/template`, `/setup`, `/appeals`, `/help`, appeal form and queue, command sync, guild lifecycle, case publications, and cached directory lookups. |
| `discordtext` | Transport-free Discord prose helpers: `{{quack:key}}` icon placeholders and the generated emoji catalog, Markdown escaping, quoting, the conversation layout. |
| `api` | The dashboard's `net/http` API: middleware, sessions, OAuth, handlers over `quack.Services`. |
| `worker` | In-process action queue, the database poller behind it, periodic loops. |
| `modules` | Shared module plumbing: per-guild toggles and settings, module actor, guild ID mapping, audit adapter, bounded work pool. |
| `modules/tickets`, `modules/logging`, `modules/honeypot` | The optional modules, each with its own domain, storage, Discord glue, and routes. |
| `v4import` | Parses and validates v4 case exports. |
| `app` | Composition root. |
| `contract` | Reflects the API route table into OpenAPI. Only `cmd/openapi` and tests import it, so the server binary does not carry the reflector. |
| `testutil` | Shared test stores (in-memory SQLite plus miniredis). |

### Startup and shutdown

`app.Run` (`app/app.go`):

1. Opens MySQL and Redis and applies pending migrations (`store.Migrate`).
2. Creates the Discord session (`discord.New`).
3. Wires the worker, module registry, `quack.Services`, the three modules,
   background loops, gateway intents, guild lifecycle and module gateway
   handlers, the interaction router (with each module's components and
   `/setup` subcommand), and the HTTP server.
4. Syncs slash commands (`discord.SyncCommands`). Definitions are
   fingerprinted in Redis so unchanged commands are not rewritten, and each
   command's ID is recorded so copy like `/case view` renders as a clickable
   mention. Only `dev` registers `/ui-preview`, a message design gallery.
5. Starts the worker and the logging and honeypot pools.
6. Opens the Discord gateway.
7. Serves HTTP until SIGINT or SIGTERM.

Background loops (`Worker.Every`): appeal notifications every 5s, audit
mirror every 5s, case publication refresh every 2s, ticket transcript sweep
hourly, honeypot upkeep and honeypot warnings every second, and the launch
announcement every 2s when `discord.launch_announcement` is on.

On shutdown the HTTP server drains first, then the rest stop newest first
(gateway, honeypot pool, logging pool, worker, Redis, MySQL), all within one
`api.shutdown_timeout`. The worker stops polling and drains cases already
queued.

**Gateway intents** are fixed at connect time, so Quack always requests
`Guilds` plus everything its modules can use (`gatewayIntents`): logging
needs Guild Members, Guild Moderation, Guild Messages, and Message Content;
honeypot needs Guild Messages; tickets need Guild Members, Guild Messages,
and Message Content for the message journal. A module switched on with
`/setup` hears events immediately. Guild Members and Message Content are
privileged: enable both in the Discord developer portal or the gateway
refuses to connect.

## How it works

### Creating a case

```
Discord ──> discord.Router ──> /case add handler (live staff context, authorize)
                                  │
                                  v
                       quack.CaseService.Create
                        │ idempotency replay check
                        │ preflight outside the lock: template, level, live
                        │   target/bot/hierarchy checks, context, evidence capture
                        v
               store.WithGuildCaseLock (SELECT ... FOR UPDATE on the guild)
                        │ re-check idempotency, re-select level, compare
                        │ one transaction: case, snapshot, event, executions,
                        │   notification, evidence, audit
                        v
               Scheduler.Submit(caseID) ──> worker ──> ActionService.ProcessCaseActions
                                                       └─> the one member notification
```

1. **Router** (`discord/router.go`). Each interaction ID is claimed in Redis
   (`discord:interaction:<id>`, 15 minutes) so a redelivery never runs twice;
   the claim fails closed when Redis is down. Commands dispatch by name,
   components and modals by the namespace and action in their custom ID
   (`namespace:action:version:payload`). Handlers answer within Discord's
   three seconds and defer slow work. Panics are recovered.
2. **Handler** (`discord/case_create.go`). Resolves the moderator's staff
   context once from live Discord state, defers, and calls
   `CaseService.Create` with source `discord`, the interaction ID as
   idempotency key, and the optional `message_link` and `file` as evidence.
   Only templates with required context fields open a modal first (more than
   five fields span several pages, with the draft in memory); optional context
   is added afterwards with Edit context. The "Add case" message action and
   "Add case for member" user action answer privately with a rule picker (25
   per page, skipped when there is one rule), then post the receipt with bot
   credentials.
3. **Preflight** (`quack/case.go`, `quack/authz.go`, `quack/evidence.go`).
   Loads the template, selects the level, re-checks Discord (actor and bot
   present; target a current human member who is not the actor, a bot, Quack,
   or the owner; target below actor and bot; both hold the action's
   permission), validates context, and captures linked messages and
   attachments.
4. **Locked write** (`store/store.go`, `store/cases.go`). Under a row lock on
   the guild the service re-checks idempotency, re-selects the level, and
   compares with the preflight; if another case for the member landed
   meanwhile it retries, up to four attempts. `store.CreateCase` assigns the
   next case number and writes everything in one transaction.
5. **Scheduling.** After commit the case ID is submitted to the worker queue.
   This is only a latency shortcut: if the queue is full or stopped, the
   poller finds the case anyway.
6. **Receipt** (`discord/case_receipt.go`). The command defers publicly and
   the receipt replaces the placeholder: case number, target, rule, reason,
   action progress, moderator, and quoted context, never evidence. Its
   buttons (Edit context, View evidence, History, Void case) recheck
   authority on every click. It is recorded as a case publication so it
   tracks later changes. On error the placeholder is deleted and the
   moderator gets a private followup.

Dashboard creation (`POST /guilds/{discordGuildID}/cases`) calls the same
`CaseService.Create` with source `dashboard` and the request's
`Idempotency-Key`. Honeypot cases use `CaseService.CreateSystemHoneypot`,
which skips staff checks but keeps every target and bot check.

### Escalation, voiding, and bootstrap

- `quack/escalation.go` picks the level. The count comes from
  `store.CountTemplateCasesForTarget` (valid, non-v4 cases for the guild,
  member, and template ID, plus one); a `case_decay_days` of 1 to 36500 sets
  `CreatedAtOrAfter`. The highest reached `trigger_case_count` wins, else the
  default level. A unique index enforces one default level per template.
- The case stores `cases.template_snapshot_json`: template version and decay
  window, selected level with the matching count, action settings, context
  fields, and submitted values. Appeal eligibility and notifications read the
  snapshot.
- `CaseService.Void` marks the case voided with a reason; corrections are a
  void plus a new case with `replaces_case_id`. Voiding cancels executions
  not yet started and fails an unsent notification; running work is left
  alone. In the same transaction it queues a reversal of each succeeded
  timeout and ban (`store.queueVoidedCaseReversals`), recording the voider as
  `requested_by` and, for an accepted appeal, `reversal_appeal_id`. A
  punishment that succeeds after its case was voided is reversed the same
  way; one that would retry fails for review instead. Before an automatic
  reversal runs, the worker checks `store.CompetingPunishmentExists` and
  `GuildService.PreflightReversal`; if either fails it goes to staff review.
- `CaseService.UpdateContext` (free text replacing the context values, with
  new message links captured) and `CaseService.AddEvidence` never touch the
  decision.
- On `GuildCreate` (`discord/lifecycle.go`), `GuildService.BootstrapDiscordGuild`
  creates or reactivates the guild, its settings, and the starter template
  (`quack/starter.go`), then ensures the evidence channel exists: a missing
  one is created hidden from everyone but Quack and staff roles; an existing
  one is left as admins set it. Saved copies are linked by their message in
  that channel, which outlives Discord's signed file URLs. Leaving a guild
  only marks it inactive. Lifecycle events are handled one at a time, since
  Discord sends a `GuildCreate` for every guild on connect, and evidence
  channel setup waits out Discord rate limits instead of failing.

### Action engine

Each enforcement is a row in `case_action_executions`; each try is a row in
`case_action_attempts`. Types are `timeout_user`, `kick_user`, `ban_user`,
and the reversals `remove_timeout` and `unban_user`.

```
pending ──claim──> running ──ok──────────────> succeeded
   ^                  ├─error, safe to retry──> retrying ──(backoff)──> running
   │                  └─error otherwise, or ──> failed ──dismiss──> (dismissed, kept)
   │                    lease expired             │
   └──────────────────staff retry─────────────────┘
pending/retrying ──case voided──> cancelled
```

- **Leases.** `store.ClaimNextCaseAction` takes the case's next due execution
  in position order, marks it running with a fresh two-minute lease token,
  and opens an attempt. Nothing is claimed while another worker holds a live
  lease on the same case. Completion is fenced by the token.
- **Expired leases** fail with `lease_expired` for staff review and are never
  retried automatically, since Discord may already have applied the action.
- **Automatic retry** (`quack/action.go`) needs a retryable classified error,
  a certain outcome, a `safe_for_retry` execution, and attempts within
  `max_retries`. Unclassified errors count as uncertain. Template actions are
  safe to retry with a one-second backoff; reversals are not.
- **Attempts.** Each Discord call has a 30-second timeout. The request,
  redacted response, and error code are stored with a case event and an
  audit entry.
- **Staff controls**, in the dashboard (`/guilds/{discordGuildID}/action-failures/...`)
  and Discord (`/case failures`, `/case retry`, `/case dismiss`,
  `/case reverse`): retry re-runs the live preflight and requeues; dismiss
  leaves the queue with history kept; reverse queues `remove_timeout` or
  `unban_user` against a succeeded timeout or ban after
  `GuildService.PreflightReversal` (an unban target may have left; kicks
  cannot be reversed). Every control is audited, denials included.

**Notifications** (`case_notifications`, one per case, created with the case
when the level has `notify_user`):

- Before a kick or ban the worker opens the DM channel while the member still
  shares a guild (`pending` to `prepared`).
- Once no execution is pending, running, or retrying, the notification is
  leased (`claimed`), moved to `sending` just before the DM, and finished as
  `sent` or `failed`. `sending` is never reclaimed, so a crash cannot send
  twice. Failures are recorded and not retried.
- `quack/notify.go` builds the plain message (capped at 2000 characters). A
  messenger implementing `quack.CaseNotificationSender` gets a structured
  `CaseNotificationRequest` instead and renders it; the Discord bot does
  (`discord/notify.go`), adding an "Appeal decision" button
  (`appeal:submit:v1:<case ID>`) for appealable cases. A plain messenger adds
  a dashboard appeal link when a dashboard URL is configured.

**Polling** (`worker/worker.go`). A bounded channel (`queue.size`) is drained
by `queue.workers` goroutines, and a case already waiting is not queued
twice. Every second the poller asks `store.ListExecutableCaseIDs` for up to
100 cases with due pending or retrying executions, running executions with
expired leases, or notifications that are pending, prepared, or claimed with
an expired lease on cases with no active executions. Results interleave
across guilds so one busy guild cannot starve the rest. The database is the
source of truth.

### Appeals

`quack.AppealService` (`quack/appeal*.go`):

- **Eligibility.** The signed-in user must be the case's target (guild
  membership not required). The case must be valid, its snapshot appealable,
  and without an appeal. `CanSubmit` checks this before a Discord form opens.
- **Statement.** One fixed question; the answer is `statement`, 1 to 4000
  characters, trimmed.
- **Statuses.** `pending`, `needs_information`, `accepted`, `rejected`,
  `closed`. Staff request information (pending to needs_information) and the
  member's reply returns it to pending. Staff accept or reject a pending
  appeal, close a pending or needs_information one, and reopen a rejected or
  closed one (to needs_information).
- **Accepting** records the decision and voids the case in one transaction,
  queuing reversals linked to the appeal. Any succeeded timeout or ban still
  without a reversal can be reversed through
  `POST /guilds/{discordGuildID}/appeals/{appealID}/reversals`
  (`ActionService.ReverseForAppeal`, full live preflight).
- **Settings** in `guild_settings`: the appeal queue channel (separate from
  the audit mirror), an optional rejoin invite sent with accepted appeals,
  and whether decisions need a reason (`ReviewReasonRequired`).
- **Notifications.** Every change writes an `appeal_notifications` row in the
  same transaction. `quack.AppealNotificationDispatcher` leases and sends up
  to 50 every five seconds; a row moves to `sending` before it goes out and is
  never reclaimed, and `delivery_deferred` rows retry after a minute. Member
  decisions carry a frozen `AppealDecisionIntent`. Staff get one queue post
  per appeal: a notifier implementing `quack.AppealQueuePublisher` posts once
  and edits in place (`delivery_channel_id`, `delivery_message_id`,
  `refresh_requested`). Messages never name the staff member.
- **In Discord** (`discord/appeal_*.go`, `discord/appeals.go`).
  `discord.AppealNotifier` implements both rich interfaces. The queue post
  pages the statement, offers Accept and Reject while pending (with a reason
  form when required) and "Confirm ..." buttons for reversals still on offer,
  and links the dashboard. `/appeals` pages pending appeals from storage.
  Every staff control re-reads live permissions. The decision DM quotes the
  reason, adds a Rejoin Server button when there is an invite, and links the
  member's appeal page, where they reply to information requests.

### Audit log and mirror

- `audit_log_entries` is append-only: `store.New` installs GORM callbacks
  that refuse updates and deletes. Metadata and failure text are redacted on
  write (`quack.RedactAuditMetadata`).
- Changes write their audit entry in the same transaction. Denials are
  audited (`authorization.denied`, or the read's own action such as
  `case.read` with result `denied`); successful reads are not, though older
  read rows remain.
- `GET /guilds/{discordGuildID}/audit-log` reads with filters and a
  `before_id` cursor. `GET .../statistics` is computed from cases,
  executions, appeals, and audit rows; there is no statistics table.
- **Mirror.** Writing an entry in `quack.ImportantAuditActions` in a guild
  with a mirror channel also queues a row in `audit_mirror_deliveries` in the
  same transaction. `quack.AuditMirror` polls every five seconds, claims up
  to 50 due rows (one per guild before any guild gets a second), and posts
  them. Case-related entries carry the case number, target, rule, level and
  outcome (on `case.create`), whether a reversal found the punishment already
  over, and the retryable execution. The Discord entry
  (`discord/views_audit.go`) is one line with subtext details, a "Retry
  action" button for retryable failures (same checks as `/case retry`), and a
  link to the relevant dashboard page.
- **Delivery.** Rows are leased and marked sending just before the Discord
  call. A lapsed claim is retried; a lapsed send fails with
  `delivery_outcome_unknown` and is never retried, so a crash can lose a post
  but never duplicate one. Failures back off 1m, 5m, 30m, 2h, 6h; the sixth
  gives up. After one failed send, the rest of that guild's batch fails
  without calling Discord.
- The mirror writes audit entries only for staff-relevant events:
  `audit_mirror.repaired` when the channel is gone (setting cleared,
  remaining rows skipped) and `audit_mirror.failed` once per outage.

### Case publications

Public Discord messages about a case, such as the `/case add` receipt, are
recorded in `case_publications` (`CasePublicationService.Record`) and edited
with bot credentials, so they outlive the interaction token. Every
transaction that changes what a receipt shows sets `refresh_requested`,
clears `last_digest`, and bumps `revision` (`store.requestPublicationRefresh`).
`discord.PublicationRefresher` runs every two seconds: it renders due
publications, skips edits whose digest is unchanged, reports `Complete`, or
`Retire`s publications whose message or case is gone. Settling receipts are
rechecked in 2s, failed refreshes in 30s. Completion is fenced on `revision`,
so a change committed during a refresh is never lost.

### Discord messages and dashboard links

Every message uses one plain-text layout, `discordtext.Conversation`: icon
and lead sentence, optional quote, details, and a `-#` subtext line. Views
write `{{quack:key}}` icon placeholders and command references; the router,
responder, and `Bot.Send` resolve both for the sending application
(`Message.ForApplication`). Emoji IDs come from
`discordtext/icons_generated.go`, generated from
`assets/icons/quack/manifest.json` (`go generate ./internal/discordtext`,
needs Node); an application without uploads gets the same text without
icons. Overlong content keeps its leading paragraphs and attaches the rest as
`message.txt`. Mentions are suppressed unless allowed, link previews hidden,
and ephemeral flags dropped in DMs. Messages with buttons end with the
invisible `spacer` icon.

Slash commands answer in the channel. Buttons that open something new and
the forms they open, plus Retry, Dismiss, and reversals on shared messages,
answer privately. Paging buttons edit their own message. Custom IDs are part
of messages already posted in Discord, so never rename them.

Messages with a matching dashboard page carry a link button built by
`quack.DashboardLinks` (`quack/dashboard.go`) from `config.Config.DashboardURL`.
Path segments must be opaque IDs or page names, and links never carry
evidence, context, or other text. Without a dashboard, or when a message
already uses Discord's five rows, the link is omitted. These paths are
therefore a stable public URL map:

| Path | Who | Linked from |
| --- | --- | --- |
| `/guilds/{discordGuildID}` | staff | `/help` overview |
| `.../cases` | staff | `/case list`, `/help topic:cases` |
| `.../cases/{caseID}` | staff | `/case view`, evidence pages, the receipt, case audit entries |
| `.../members/{discordUserID}` | staff | `/case user`, History |
| `.../appeals`, `.../appeals/{appealID}` | staff | `/help topic:appeals`, queue post, `/appeals`, appeal audit entries |
| `.../failures` | staff | `/case failures` while failures remain |
| `.../rules`, `.../rules/{templateID}` | staff | `/help topic:rules`, `/template` results, rule audit entries |
| `.../settings` | staff | `/setup appeals`, `/setup audit`, settings audit entries |
| `.../modules/{tickets,logging,honeypot}` | staff | the module's `/setup` and audit entries |
| `/guilds/{guildID}/cases/{caseID}/appeal` | the case's member | case DM, appeal submitted, decision and information-request DMs (internal IDs) |

### HTTP API

`api.Server` (`api/api.go`, routes in `api/routes.go`) uses stdlib
`net/http` with Go 1.22 `ServeMux` patterns.

**Contract.** Every route is registered with an `api.Doc` (module routes with
a `modules.Doc`) naming its operation ID, summary, and the Go types it reads
and writes (query struct with `query` tags decoded by `modules.DecodeQuery`,
JSON body, success status and body, error statuses), plus its
`api.Protection` (credential, rate limit, guild authorization, idempotency),
from which session, CSRF, `Idempotency-Key`, and protection error statuses
are derived. `Server.mount` is the only place routes reach the mux.
`internal/contract` reflects the table with `swaggest/openapi-go`;
`cmd/openapi` writes the file from `app.Routes`. Tests fail when the
committed file is stale, and `TestContractCoversEveryMountedRoute` checks the
served and documented operations match.

**Global middleware**, in order:

1. Trace IDs: adopts or generates `X-Request-ID` and `X-Correlation-ID`.
2. Observation: recovers panics as 500s, logs one line with the route
   pattern (never the raw path or query), and rewrites errors into
   `{"error": {"code", "message", "request_id", "correlation_id"}}`.
3. Security headers: deny-all CSP, `nosniff`, `X-Frame-Options: DENY`,
   `no-referrer`, `no-store`.
4. CORS: exactly `api.cors_origins` with credentials; any other `Origin` is
   403.
5. Body limit: `api.max_body_bytes`.
6. CSRF: cookie-authenticated writes need an allowed `Origin` and the CSRF
   cookie echoed in `X-CSRF-Token`. Bearer-token callers skip it.

Unknown paths and wrong methods both return 404.

**Guild staff routes** then add: pagination bounds and a rate limit keyed by
credential and guild (reads `limits.member_read`, writes
`limits.template_write`, case creation `limits.case_create` plus
`limits.evidence`, retries and reversals `limits.retry`); the session from
cookie or bearer token (Redis, sliding expiry; an expired session or Discord
grant returns `reauthentication_required`); the guild context from
`GuildService.ResolveStaffContext` plus `Authorize` for the route's
capability (guild and staff rows written only on change, `last_active_at` at
most every five minutes); and for writes a required `Idempotency-Key`, held
as a fenced Redis lease during the write and then replayed for
`api.idempotency_ttl` (a different body is 409). Member routes
(`/members/me/...`) use the signed-in user instead of a guild context.
Appeal writes check the capability before idempotency so a demoted reviewer
cannot replay. Rate limits and idempotency fail closed with 503 when Redis
is down.

**Live authorization.** `Bot.GuildAuthorization` (`discord/client.go`,
`discord/live.go`) needs the guild owner and roles and the current roles of
actor, bot, and target. While the gateway is live these come from
discordgo's state, kept current by `GUILD_UPDATE`, `GUILD_ROLE_*`,
`GUILD_MEMBER_UPDATE`, and `GUILD_MEMBER_REMOVE`, so a typical request makes
no REST calls. REST fills gaps (disconnected, before `READY`, unloaded
guilds, members a large guild did not send). REST-fetched members sit in a
small overlay for up to 30s (10,000 entries), dropped on any member event for
that user and on `READY`, never written into discordgo's state, and "not a
member" is never cached. A state member without a join time came from a
presence update and is fetched instead.

**Directory routes** (`api/directory.go`) let the dashboard show names,
avatars, and channels instead of snowflakes:
`GET /guilds/{discordGuildID}/directory/members?query=` (`case.read`),
`.../directory/users?ids=` for up to 100 IDs (`case.read`), and
`.../directory/channels` in sidebar order (`guild_settings.read`). `app`
implements `api.Directory` over the Discord adapter (`discord/directory.go`),
caching users and members per guild for 10 minutes (5000 entries, misses
fetched 8 at a time) and channels for 30 seconds. Discord failures are 502,
Discord rate limits 503, and a server without a directory answers 503.

**OAuth** (`api/oauth.go`). `GET /auth/discord/login` stores a single-use
state in Redis bound to a browser cookie; `GET /auth/discord/callback`
exchanges the code and creates the session. Tokens stay server-side.
`GET /auth/me`, `POST /auth/logout`, and `POST /auth/logout-all` complete
the set.

### Optional modules

The modules plug into the core the same way:

- **Toggles and settings.** `modules.Registry` stores `module_configurations`
  (one row per guild and module: `enabled` plus opaque JSON settings) and
  implements `quack.ModuleToggles`, so the core settings API's
  `tickets_enabled`, `general_logging_enabled`, and `honeypot_enabled` read
  the same rows. Before enabling, the settings service asks
  `quack.ModuleEnablementChecker`: a module never set up is refused ("run
  /setup first"); otherwise its `EnablementCheck` re-runs the `/setup` checks
  against live Discord without writing.
- **Setup.** `/setup` (`discord/setup.go`) needs Manage Server, checked live,
  and answers publicly. `appeals` and `audit` are core settings (queue
  channel, rejoin invite, required reason; mirror channel). `tickets`,
  `honeypot`, and `logging` go to each module's `Router.HandleSetup`
  handler; with only `enabled` they toggle through the settings service and
  keep saved setup. Without a channel option, `discord.SetupChannel` reuses
  the configured channel or creates one with permissions, a topic, and an
  introduction, replacing a configured channel only when Discord says it is
  gone.
- **HTTP.** `MountHTTP` adds routes under
  `/guilds/{discordGuildID}/modules/` through `api.ModuleMux`, with the
  endpoint policy, session, live guild context, a per-actor limit, and
  required `Idempotency-Key` on writes.
- **Discord and background work.** `RegisterGateway` subscribes to events;
  gateway work runs in a bounded `modules.Pool` that drops jobs when full, so
  module traffic never delays moderation. Module events reach the audit log
  through `modules.AuditLog`.

Per module:

- **Tickets** (`modules/tickets`, routes `/modules/tickets/...`).
  `/setup tickets` saves an entry channel and a staff queue channel and posts
  the "Need a hand?" panel with an Open ticket button, edited in place or
  moved on re-setup. Opening creates a private thread under the entry
  channel and a best-effort staff queue post. `ticket_message_journal`
  records each message's original text; closing refuses while the journal is
  incomplete, then locks the thread, builds a transcript from journal plus
  surviving history, updates or reposts the queue entry with a `.txt`, DMs the
  member a copy, and deletes the thread. Closed tickets cannot be reopened
  (the reopen route always answers 409). An hourly sweep deletes transcripts
  and journal text past retention. Tables: `tickets`, `ticket_events`,
  `ticket_transcripts`, `ticket_member_states`, `ticket_message_journal`.
- **General logging** (`modules/logging`, routes `/modules/general-logging/...`).
  Posts message edits and deletes, joins and leaves, bans, and guild and
  channel changes as readable Quack messages. `/setup logging` routes every
  event to one channel and needs View Audit Log to attribute bans. Recent
  messages are cached in memory only; no tables. Deleting a channel removes
  any routes to it, and leaves settings alone when none use it. Nothing the
  module does, including settings changes and deleted-channel repair, writes
  to the audit log or its mirror.
- **Honeypot** (`modules/honeypot`, routes `/modules/honeypot/...`). A post
  in the trap channel opens a case with the configured template through
  `CaseService.CreateSystemHoneypot`, then the bait message is deleted via
  `honeypot_message_cleanups`. Staff, Quack, and webhooks are ignored (other
  bots are not), a burst from one member is one incident, and
  `honeypot_triggers` makes each message fire at most once; the upkeep loop
  finishes interrupted incidents without punishing twice. `/setup honeypot`
  creates the template with `TemplateService.EnsureHoneypotTemplate` if
  needed and posts a warning with a live "N incidents caught." counter,
  refreshed and reposted through `honeypot_warning_refreshes`. The API tells
  the module when a template is updated or archived.

### Storage

- `store.Store` implements every storage port over GORM/MySQL and Redis. Each
  moderation change commits in one transaction with its timeline events and
  audit entries. IDs are ULIDs; one record struct per table lives in
  `store/schema.go`.
- Work and outbox tables sit beside the history they serve and are updated
  as work progresses: `case_action_executions`, `case_notifications`,
  `appeal_notifications`, `case_publications`, `audit_mirror_deliveries`.
- Redis holds sessions and OAuth state (`auth:*`), interaction dedupe claims,
  the command sync cache, rate limit counters, and idempotency records.
- `store.New` accepts a nil Redis client for MySQL-only tools (`migrate`,
  `import-v4`).

## Dashboard

`apps/dashboard` is the React app and the Go program that serves it. Its
[README](../apps/dashboard/README.md) covers frontend conventions and
day-to-day commands.

- **App.** Vite+ (`vp`) builds a React app styled with CSS Modules. TanStack
  Router owns URLs (file routes in `src/routes`, code-split), TanStack Query
  owns server state, TanStack Table renders lists. The UI calls templates
  "rules".
- **Server.** `quack-dashboard` (`main.go`, `server.go`) embeds the build,
  gzips it once at startup, serves fingerprinted assets as immutable, sets a
  strict CSP, falls back to `index.html`, and reverse-proxies `/api/*` to the
  API with the prefix removed. `GET /healthz` is its health check. It is
  stateless; run as many as you like.
- **One origin.** The browser only talks to the dashboard, so the API's
  host-only session and CSRF cookies land there, writes pass the `Origin`
  check, and no CORS request happens.
- **Contract.** Every call is typed from `contracts/http/openapi.yaml`;
  `bun run api` regenerates `src/api/schema.gen.ts`, and CI fails when it is
  stale. The client adds `X-CSRF-Token` and a fresh `Idempotency-Key` to
  every write. `useCan(guildId)` reads `GET /guilds/{id}/me` to hide what the
  user cannot do; the API checks again regardless. User IDs rendered in one
  tick are batched into one directory call.
- **Sign-in.** The app links to `/api/auth/discord/login?redirect_to=<page>`.
  Discord redirects to `discord.oauth_redirect_uri`, which must be the
  dashboard's `/api/auth/discord/callback` so the cookie lands on the
  dashboard's origin. The API redirects back, and the app reads
  `GET /api/auth/me` for the user and CSRF token. A request that finds the
  session gone sends the user to `/login` and back afterwards.

| Variable | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `QUACK_DASHBOARD_PORT` | `-addr` | `3000` | Listen port (`-addr` takes a full address). |
| `QUACK_DASHBOARD_API_URL` | `-api` | `http://localhost:8080` | Where `/api` is proxied. |
| `QUACK_DASHBOARD_DIR` | `-dir` | embedded build | Serve a build from disk instead. |

The API side, for a dashboard at `https://dashboard.example.com`, needs
`QUACK_API_CORS_ORIGINS=https://dashboard.example.com`,
`QUACK_DISCORD_OAUTH_REDIRECT_URI=https://dashboard.example.com/api/auth/discord/callback`,
and `QUACK_API_TRUSTED_PROXIES` set to the dashboard's address or network, so
per-IP sign-in limits apply per user (the dashboard forwards
`X-Forwarded-For`).

## Configuration

Settings come in three layers, each overriding the last:

1. Code defaults (`config.Default`, `apps/backend/internal/config`).
2. An optional TOML file: `-config <file>`, else `QUACK_CONFIG`, else
   `quack.toml` in the working directory (only that default may be missing).
   `apps/backend/quack.example.toml` lists every key.
3. Environment variables `QUACK_<SECTION>_<KEY>`. The nearest `.env`
   (searching up from the working directory) is read first; real environment
   variables win over it.

Unknown keys in the file, or in a known section's variables, fail startup.
`quack serve` validates everything and reports every problem at once;
`migrate` and `import-v4` need only `database.dsn`.

Variable names: uppercase the TOML key, replace the dot with an underscore,
prefix `QUACK_` (`api.cors_origins` is `QUACK_API_CORS_ORIGINS`;
`environment` is `QUACK_ENVIRONMENT`). Lists are comma-separated, durations
are Go durations (`15s`, `168h`), rate limits are `<max>/<window>`
(`120/1m`), and an empty variable counts as unset. `QUACK_CONFIG` and the
test-only `QUACK_TEST_*` variables are not settings. Keep secrets
(`discord.token`, `discord.client_secret`, `api.ops_token`,
`api.metrics_token`, `database.dsn`) in the environment.

"Prod" below means `staging` or `production`.

| Key | Default | Meaning |
| --- | --- | --- |
| `environment` | `dev` | `dev`, `test`, `staging`, or `production`. Outside dev, `auth.cookie_secure` defaults to true and `api.cors_origins` to empty. Dev logs are colored text; others JSON. |
| `api.port` | `8080` | HTTP listen port. |
| `api.cors_origins` | localhost:3000 and 127.0.0.1:3000 in dev | Exact dashboard origins for credentialed requests; required outside dev; wildcards fail. The first `https` origin (in dev, the first `http` too) is where Discord messages link; with none, messages have no dashboard buttons. |
| `api.trusted_proxies` | none | Proxy IPs or CIDRs whose forwarded client IP is trusted. |
| `api.max_body_bytes` | `1048576` | Largest request body. |
| `api.read_header_timeout` / `read_timeout` / `write_timeout` / `idle_timeout` | `5s` / `15s` / `30s` / `1m` | HTTP server limits. |
| `api.shutdown_timeout` | `20s` | Total graceful shutdown budget: HTTP drain then everything else. |
| `api.idempotency_ttl` | `24h` | How long a completed write can be replayed. |
| `api.ops_token` | none | Enables `GET /ops/status` and operator access to guild ops status via `X-Quack-Ops-Key`. Required in prod. |
| `api.metrics_token` | none | Required in `X-Quack-Metrics-Key` for `GET /metrics` (404 without it). Required in prod. |
| `auth.session_cookie_name` | `quack_session` | Session cookie. |
| `auth.csrf_cookie_name` | `quack_csrf` | Double-submit CSRF cookie. |
| `auth.session_ttl` | `168h` | Session lifetime. |
| `auth.state_ttl` | `10m` | How long an OAuth login may take. |
| `auth.post_login_redirect` | `/` | Post-login target when none was given. |
| `auth.cookie_secure` | false in dev, else true | `Secure` cookies; must be true outside dev. |
| `discord.token` | none | Bot token. Required. |
| `discord.app_id` | none | Application ID. Required. |
| `discord.client_secret` | none | OAuth client secret. Required in prod. |
| `discord.oauth_redirect_uri` | none | The dashboard's `/api/auth/discord/callback`. Plain `https` in prod. |
| `discord.oauth_scopes` | `identify guilds` | Space-separated; must include both defaults in prod. |
| `discord.command_guild_id` | none | Sync commands to this guild only (instant updates). |
| `discord.command_prune` | `false` | Delete registered commands Quack no longer defines. |
| `discord.launch_announcement` | `false` | Post the one-time "Quack v5 is here" message to each server that hasn't had it (audit channel, else community updates or system channel, else owner DM), a few servers every two seconds. Turn on for launch only. |
| `limits.oauth` | `20/10m` | OAuth login and callback, per client IP. |
| `limits.member_read` | `120/1m` | Dashboard reads, and the per-actor limit on member, appeal review, and module routes. |
| `limits.template_write` | `30/1m` | Other dashboard writes. |
| `limits.case_create` | `20/1m` | Case creation. |
| `limits.retry` | `10/1m` | Retries and reversals. |
| `limits.evidence` | `20/1m` | Second limit spent by case creation. |
| `database.dsn` | none | MySQL DSN, e.g. `user:pass@tcp(host:3306)/quack?charset=utf8mb4&parseTime=True&loc=Local`, or a `mysql://user:pass@host:3306/quack` URL (port defaults to 3306; the query takes DSN parameters). Required. |
| `redis.url` | none | e.g. `redis://host:6379/0`. Required for `serve`. |
| `queue.size` | `1000` | Action queue buffer. |
| `queue.workers` | `3` | Action queue workers. |
| `log.level` | `info` | `debug`, `info`, `warn`, or `error`. |

**Discord application.** Use a separate application for development, with
credentials only in your local `.env`. The install URL needs the `bot` and
`applications.commands` scopes. The bot needs View Channel, Send Messages,
Embed Links, and Read Message History; Moderate Members, Kick Members, and Ban
Members for whichever actions templates use (case creation is refused up
front without them); and Manage Channels for the evidence and ticket
channels. Enable the Guild Members and Message Content privileged intents
(see [Startup and shutdown](#startup-and-shutdown)); without Message Content,
Discord also blanks message text in REST responses, which breaks evidence
capture. Guild ops status reports a guild as degraded when the bot lacks
Moderate Members, Kick Members, Ban Members, or Manage Channels.

## Development

You need Go (the version in `apps/backend/go.mod`), Bun, Docker, and a
development Discord application.

1. Copy `.env.example` to `.env` and fill in `QUACK_DISCORD_TOKEN`,
   `QUACK_DISCORD_APP_ID`, and `QUACK_DISCORD_CLIENT_SECRET`. Setting
   `QUACK_DISCORD_COMMAND_GUILD_ID` to a test guild applies command changes
   instantly.
2. `docker compose up -d` starts MySQL 8.4 and Redis (`compose.yaml`).
3. From `apps/backend`: `go run ./cmd/quack serve`, or `air` to rebuild on
   change (`.air.toml`). The API listens on `http://localhost:8080`.
4. From `apps/dashboard`: `bun install && bun run dev`, serving
   `http://localhost:3000` and proxying `/api`. Register
   `http://localhost:3000/api/auth/discord/callback` as an OAuth2 redirect and
   set `QUACK_DISCORD_OAUTH_REDIRECT_URI` to match.

`docker compose --profile app up --build` also runs the API and the
dashboard in containers, reading `.env` with the storage addresses replaced
by the `mysql` and `redis` service names.

Go commands run from `apps/backend`; `go.work` at the root covers it and the
dashboard server.

| Command | What it does |
| --- | --- |
| `go run ./cmd/quack serve` | Bot, API, and workers (the default command). |
| `go run ./cmd/quack migrate up` / `down` | Apply pending migrations, or roll back the newest. |
| `go run ./cmd/quack import-v4 ...` | See [v4 import](#v4-import). |
| `go run ./cmd/quack help` | Usage; `quack <command> -h` lists flags. |
| `gofmt -l .`, `go vet ./...`, `go test ./...` | Format check, vet, tests. |
| `go build ./cmd/quack` | Build the binary. |
| `go generate ./...` | Regenerate `contracts/http/openapi.yaml` and the icon catalog (needs Node). `go run ./cmd/openapi` prints the contract. |
| `staticcheck ./...` | Optional. |
| `bun run check` / `test` / `build` / `api` | Dashboard checks, tests, build, and type regeneration (from `apps/dashboard`). |

CI: `.github/workflows/go.yml` builds `./cmd/quack` and runs `go test ./...`;
`.github/workflows/dashboard.yml` checks the dashboard's API types against
the contract, then lints, tests, and builds the app and its Go server.

**Where things go.**

- Business rules: `internal/quack`, behind `quack.Services`.
- Storage: `internal/store`. Schema changes are a new migration in
  `store/migrate.go` plus record structs in `store/schema.go`.
- HTTP routes: `internal/api/routes.go`, handlers by topic (`cases.go`,
  `appeals.go`, ...). Give each route an `api.Doc` (or `modules.Doc`) with
  named types, never `map[string]any` or anonymous structs. After changing a
  route or any type it uses, run `go generate ./...` in `apps/backend` and
  `bun run api` in `apps/dashboard`, and commit both. Never edit the YAML by
  hand; use `go test -count=1` if only the YAML changed.
- Discord commands and components: `internal/discord`. Do not rename custom
  IDs.
- Background loops: `Worker.Every` in `internal/app/app.go`.
- Module features: the module's package under `internal/modules/`, wired in
  `internal/app/app.go`.
- Settings: `internal/config` (struct, default, validation), plus
  `quack.example.toml` and the table in [Configuration](#configuration).

Code follows [Google Go style](https://google.github.io/styleguide/go/) and
[Go doc comments](https://go.dev/doc/comment); `AGENTS.md` has the agent
rules. The v4 bot source is in Git history; the importer does not need it.

## Testing

From `apps/backend`: `gofmt -l .`, `go vet ./...`, `go test ./...`. Start
with the narrowest package (`go test ./internal/quack -run Escalation`), and
run `go test -race ./...` after touching the worker, router, or anything with
locks or leases.

- **Storage.** Most tests use a real `store.Store` over in-memory SQLite with
  the full schema, plus miniredis (`internal/testutil/store.go`). SQLite holds
  a single connection, so a stray query outside the current transaction
  hangs instead of passing by accident.
- **Discord.** Fakes stand in for the Discord ports and the router's
  interaction client; nothing talks to Discord. `/case` command definitions
  and rendered messages are pinned by golden files in
  `internal/discord/testdata/`; an unexpected custom ID diff is a real break.
- **HTTP.** `internal/api` tests drive the real `api.Server` with `httptest`,
  pinning routes, statuses, the error envelope, cookies, CSRF, and
  idempotency.
- **Config.** Tests pass the environment explicitly.
- **Fuzz.** `internal/quack/fuzz_test.go` fuzzes template policy JSON and
  context values (`go test ./internal/quack -fuzz <name>`).
- **MySQL.** SQLite differs on locking, generated columns, and the migration
  lock, so `TestMySQL` (`internal/store/migrate_mysql_test.go`) and
  `TestMySQL*` (`internal/quack/mysql_integration_test.go`) run against real
  MySQL when `QUACK_TEST_MYSQL_DSN` is set. Each creates and drops its own
  database, so the user needs `CREATE` and `DROP`. CI does not set it:

  ```sh
  QUACK_TEST_MYSQL_DSN='root:quack-root@tcp(127.0.0.1:3306)/?parseTime=true' \
    go test ./internal/store ./internal/quack -run MySQL
  ```

## Migrations

The ordered list lives in `apps/backend/internal/store/migrate.go`; each
applied migration is recorded in `quack_schema_migrations` by version and
name. `quack migrate up` (or `quack migrate`) and `quack serve` both apply
what is pending. On MySQL migrators take a named lock (`GET_LOCK`), so
several processes can start at once.

| Version | Name | Notes |
| --- | --- | --- |
| 1 | `baseline` | Creates every table, including module tables (`modules.Models`, `tickets.Models`, `honeypot.Models`), via AutoMigrate, then adds one default level per template: a unique index on a generated `default_template_id` column on MySQL, a partial unique index on SQLite. The schema is unreleased, so the baseline is edited in place: re-running it adds missing tables and columns, and `retireAppealForms` cleans up leftover custom appeal form data. |
| 2 | `audit_mirror_deliveries` | The mirror's queue table. Nothing is backfilled, so entries waiting at upgrade time are not mirrored. No `down`: an older binary would re-post everything. `migrate down` stops here, even with `-drop-all`. |
| 3 | `launch_announcement` | Adds `guild_settings.launch_announced_at`. Its `down` drops the column, so roll back only before the announcement has gone out. |

**Adding one.** Append `migration{version, name, up, down}` with the next
version. Never edit or reorder a shipped migration; the ledger refuses
databases it does not recognize. MySQL commits DDL immediately, so write `up`
to detect work it already did. Leave `down` nil when undoing is unsafe.

**Rolling back.** `quack migrate down` undoes the newest migration or fails
when it has no `down`. Undoing the baseline drops all data and needs
`quack migrate -drop-all down`.

**Releasing.** Back up MySQL and verify the backup; run `quack migrate up`
with the production DSN (or let one process migrate on start); check
`quack_schema_migrations` without editing it; then start the rest and confirm
`/readyz` reports the schema version.

**If a migration fails.** Keep the new binary out of service, leave the error
and ledger as they are (never mark a migration applied by hand), inspect the
database since MySQL DDL survives, fix the migration so it resumes from that
state, and rerun `quack migrate up`. Restore the backup only when it cannot
resume.

## Operations

### Deploying

- `apps/backend/Dockerfile` builds a static `quack` on Alpine, runs as
  non-root, exposes 8080, and runs `quack serve`. Build the dashboard image
  from the repo root (it uses `assets/icons`):
  `docker build -f apps/dashboard/Dockerfile -t quack-dashboard .`; it runs
  as non-root on 3000.
- Quack needs MySQL 8 and Redis with persistence (Compose uses
  `--appendonly yes`).
- Run exactly one `serve` process per Discord application (see
  [Known gaps](#known-gaps)).
- Staging and production need `discord.client_secret`, `api.ops_token`,
  `api.metrics_token`, an `https` `discord.oauth_redirect_uri`, exact
  `api.cors_origins`, and secure cookies.
- List any proxy in front of Quack in `api.trusted_proxies`; otherwise
  forwarded headers are ignored and per-IP limits hit the proxy.
- Set the platform's termination grace period above `api.shutdown_timeout`.

### Health and monitoring

| Endpoint | Auth | Use |
| --- | --- | --- |
| `GET /livez` | none | Always 200 while serving, so outages do not cause restart loops. |
| `GET /readyz` | none | 503 unless MySQL, Redis, the gateway, the action queue, the migration ledger, and every action capability are ready. Lists each check; `migration` shows the schema version. |
| `GET /status` | none | Connectivity and latency for Discord, Redis, MySQL. Always 200; the Compose healthcheck uses it. |
| `GET /metrics` | `X-Quack-Metrics-Key` | Prometheus text. 404 when `api.metrics_token` is unset. |
| `GET /ops/status` | `X-Quack-Ops-Key` | Queue counters, worker settings, execution status counts, oldest due work, recent failures, action capabilities, across guilds. 404 when `api.ops_token` is unset. |
| `GET /guilds/{discordGuildID}/ops/status` | ops key, or owner/Administrator session | The same for one guild plus `guild_health` (missing permissions and managed channels). |

Metrics are aggregate counters with no guild, user, or content labels:
`quack_cases_total`; `quack_action_attempts_total`,
`quack_action_failures_total`, `quack_action_retries_total`,
`quack_action_retrying_current`; `quack_action_queue_depth`,
`quack_action_queue_failures_total`; `quack_notifications_total`,
`quack_appeals_total`; `quack_audit_events_total`,
`quack_audit_mirror_events_total`, `quack_audit_mirror_deliveries_total`,
`quack_audit_mirror_failures_total`; `quack_optional_module_events_total`.

Alert on sustained `/readyz` failure, growing queue depth or failures,
repeated action, notification, or mirror failures, and guilds that stay
degraded. Logs are structured, carry `request_id` and `correlation_id` (also
returned as headers), and contain IDs and outcomes, never message content,
tokens, or raw Discord errors.

### Security posture

- **Live authorization** on every guild request and interaction; cached staff
  records are only for attribution.
- **Sessions** are server-side in Redis; Discord tokens never reach the
  browser. The session cookie is HttpOnly, SameSite=Lax, and Secure outside
  dev; the CSRF cookie is readable so the dashboard can echo it; with secure
  cookies the OAuth state cookie uses the `__Host-` prefix;
  `POST /auth/logout-all` revokes every session for the user.
- **Browser boundary.** Exact CORS origins, `Origin` plus CSRF token on
  cookie writes, deny-all CSP, `no-store`.
- **Fail closed.** Without Redis, rate limits, idempotency, and interaction
  dedupe refuse work rather than skip the check.
- **Secrets.** Ops and metrics keys are compared in constant time.
- **History.** Cases and audit entries are never deleted in normal operation.
- **Errors.** A fixed envelope with stable codes; Discord and storage error
  text is never returned or stored verbatim.

### Incidents

Never hand-edit `case_action_executions`, `case_notifications`, the audit
log, the migration ledger, or Redis idempotency keys; staff controls re-check
permissions and keep fencing intact.

- **MySQL down.** Readiness fails and work waits. Restore the same database.
  After restoring an older backup, compare action attempts with the guild's
  Discord audit log before any staff retry.
- **Redis down.** Dashboard writes and logins return 503 and interactions are
  dropped. Restore Redis rather than bypass it; durable state is in MySQL,
  and lost sessions only mean signing in again.
- **Discord down or rate limiting.** Work stays in MySQL. Definite rejections
  retry within `max_retries`; uncertain failures and expired leases go to
  staff review.
- **Failed actions.** Staff use `/guilds/{discordGuildID}/failures` or
  `/case failures` to retry, dismiss, or void.
- **Degraded guild.** Check `guild_health.reasons`. Usually a lost permission
  or a deleted evidence channel, which is recreated when Discord reports the
  deletion and re-checked on the next `GuildCreate`.
- **Leaked secret.** Rotate the bot token, client secret, ops token, or
  metrics token and redeploy; revoke sessions with `POST /auth/logout-all`.
- **Bad migration.** See [Migrations](#migrations).

### Before the first real release

Rehearse in a non-production guild and application: install (starter
template and notice, evidence channel creation and repair, leave and
rejoin); permissions (owner, Administrator, Manage Guild, Moderate Members, a
former staff member, hierarchy, bot, self, and owner target checks); cases
(warning, timeout, kick, and ban from Discord and the dashboard, the receipt,
evidence, the single DM, departed-member access, appeals and reversals, the
audit mirror, retry and dismiss); each module on its own; and SIGTERM with
work in flight, confirming nothing runs twice after restart.

## v4 import

`quack import-v4` loads v4 case history into one v5 guild. Code:
`internal/v4import` (parsing, validation) and `internal/store/v4import.go`
(writes, rollback). It needs only `database.dsn`. Imported cases have source
`v4_import`, never count toward escalation, and never create actions, DMs,
notifications, or appeal work. Only cases are imported.

**Input.** JSONL, one object per line, format `quack-v4-case-jsonl/v1`, at
most 64 MiB, unknown fields rejected. Nothing in this repository produces
the export. Examples: `internal/v4import/testdata/historical_cases.jsonl`.

| Field | Required | Notes |
| --- | --- | --- |
| `format` | yes | `quack-v4-case-jsonl/v1`. |
| `source_id` | yes | Stable v4 record ID, at most 191 characters. |
| `guild_id` | yes | The v5 guild ULID (`guilds.id` for the Discord server); must match `-guild`. The bot must already have joined. |
| `target_discord_user_id` | yes | The member. |
| `reason` | yes | The v4 reason. |
| `action_type` | yes | `warning`, `timeout`, `kick`, or `ban`. |
| `created_at` | yes | RFC 3339. |
| `case_number` | no | Kept when free in the guild, otherwise remapped. |
| `moderator_discord_user_id`, `moderator_display_name` | no | Who acted in v4. |
| `context_url` | no | A Discord link. |
| `target_departed`, `target_missing` | no | The member left, or is unknown. |
| `action_expires_at` | no | When the v4 action expired. |

**Commands** (flags before arguments; all accept `-config`):

```sh
quack import-v4 import -dry-run -file guild.jsonl -source v4-final -guild 01J... -actor 123456789012345678
quack import-v4 import -file guild.jsonl -source v4-final -guild 01J... -actor 123456789012345678
quack import-v4 rollback -guild 01J... -batch v4-... -actor 123456789012345678
quack import-v4 check-scope -v4 ticket,purge -v5 case
quack import-v4 check-scope -v4 warn,timeout,kick,ban -v5 case -after-migration
```

- `import` requires `-file`, `-source` (a stable export name), `-guild`, and
  `-actor` (the operator's Discord ID, for the audit log). It prints a JSON
  report even on failure: batch ID, SHA-256 checksum, counts, per-line
  decisions, and warning and failure codes, never reasons or member IDs.
- The batch ID derives from guild, source, and checksum, so rerunning a file
  reports `AlreadyImported` and writes nothing.
- **All or nothing.** Any malformed row fails the whole file before writing;
  a real run audits the failure by code and count. Reusing a `source_id` with
  different content is `ErrSourceCollision`.
- **Warnings** (do not stop the import): `case_number_remapped`,
  `moderator_identity_unavailable`, `target_departed`, `target_missing`,
  `expired_action_manual_review`. Expired actions are never replayed.
- Batches are tracked in `v4_import_batches` and `v4_import_sources` and
  audited as `v4_import.batch`.
- `rollback` deletes the batch's cases, events, and ledger rows and writes
  `v4_import.rollback`. It refuses if any case gained rows in
  `case_action_executions`, `case_notifications`, `appeals`, or
  `case_evidence_snapshots`. Audit history is never removed.
- `check-scope` (no config or database) checks that side-by-side v4 and v5
  bots, with separate databases, Redis, and applications, have no colliding
  command names. With `-after-migration` it fails while v4 still has `warn`,
  `timeout`, `kick`, or `ban`.

## Known gaps

- **No real-guild rehearsal yet.** Install, permissions, enforcement, DMs,
  appeals, and modules have been tested against fakes, SQLite, and MySQL, but
  not end to end in a live guild. See
  [Before the first real release](#before-the-first-real-release).
- **One process only.** Multi-page `/case add` drafts live in memory, and
  every gateway-connected process receives every event, so a second `serve`
  process would split drafts and duplicate general logging posts. Quack does
  not shard.
