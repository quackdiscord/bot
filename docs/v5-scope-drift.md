# V5 Scope Drift

This document is a high-level comparison between the current backend and the product definition in [`v5.md`](../v5.md).

It is an audit, not a roadmap or implementation plan. A listed mismatch does not mean the behavior has already changed. Technical documentation should continue describing the code that exists until that code is updated.

## Already Aligned

The current backend already supports important parts of the intended v5 direction:

- Templates own levels and their actions.
- Moderators apply templates instead of choosing an escalation level.
- Case creation counts history from the selected template and snapshots the chosen result.
- Cases, action attempts, case events, and audit entries preserve moderation history.
- Discord and the dashboard call the same application behavior.
- Guild data and escalation history are isolated by guild.
- Action rows are persisted so work can recover after queue saturation or process restart.
- Discord interactions support deferred work, public case results, template autocomplete, and reusable UI components.
- Template and case reads are available for staff dashboard workflows.
- Production schema changes use an ordered, checksum-tracked migration ledger
  that adopts current v5 data additively without startup `AutoMigrate`.
- Live template contracts now use all-time distinct thresholds, archive-only
  availability, level-owned notification, and zero or one typed enforcement
  action. Frozen legacy columns remain storage-only compatibility data.
- Live case contracts now use only valid/voided validity and the dashboard,
  Discord, honeypot, and v4-import sources. Action and appeal state remain
  separate, and the official reason always comes from the immutable template
  snapshot source.
- Severity, weight, reason overrides, and free-form note lifecycle are absent
  from live case models and contracts. Migration 0003 preserves legacy columns
  and events, inventories retired event rows, and exposes none of them through
  live v5 case/event reads.
- GuildCreate bootstrap now atomically persists guild-owned channel and
  notification settings, independent optional-module enablement, the exact
  editable starter policy, and its one-time dashboard review notice. Guild
  update, leave, rejoin, and channel-deletion events preserve history while
  refreshing identity and clearing stale configured references.
- Templates now own validated ordered context definitions, reversible archive
  and restore, and confirmed policy-only import/export. Case snapshots preserve
  definitions and submitted member-visible values across versions.
- Case creation now atomically stores immutable evidence, correction links,
  optional enforcement, and exactly-one notification work. All-time escalation
  excludes voided and imported-v4 history.
- Discord timeout, kick, ban, timeout removal, and unban use classified,
  redacted outcomes. Action leases/fencing recover crashes; staff can review,
  retry, dismiss, void, or reverse without deleting history.
- Member notification is a case-level post-outcome workflow rather than a
  `send_dm` action. Evidence capture and managed attachment copies share one
  bounded HTTP/Discord service.
- The QP-D package now provides target-owned case summaries/details, the final
  case-linked appeal state machine, form snapshots, atomic acceptance/voiding,
  audited timelines, notification outbox/adapters, and explicit reversal
  offers without relying on current Discord membership.
- QI-2 installs the QP-D member/staff routes, appeal migration and outbox,
  reversal components, QP-E audit/statistics routes and mirror lifecycle, and
  QP-F honeypot runtime on the live process boundary.
- QP-G installs a privacy-safe, idempotent v4 historical-case import contract,
  non-escalating projections, command cutover validation,
  archive-only live template storage, and final storage constraints without
  coupling unfinished module history into core import.
- QP-H installs the complete dashboard/internal endpoint policy, durable Redis
  Discord dedupe, separate liveness/readiness, aggregate metrics, strict startup
  validation, redacted operational logging, and bounded shutdown contracts.

## Behavior That Must Change

No storage or integration-owned product mismatch remains in this section.

## Rejected Concepts to Remove

These concepts appear in current models, documentation, or backlog history but do not belong in the v5 product model:

- Cross-template or cross-guild escalation.
- Direct `/warn`, `/timeout`, `/kick`, and `/ban` workflows after migration.
- Generic bot utilities as requirements for the moderation core.

Historical database compatibility may require a later migration rather than immediate column removal. Keeping old data temporarily does not make the rejected concept part of the product.

The accepted QP-G storage head retires live GORM soft-delete semantics while
preserving the frozen compatibility column, converts residual legacy delete
state to archive state in logical migration 0410, and provides command-scope
checks plus the documented cutover that removes direct punishment commands.

## Missing Core Behavior

The accepted QP-G package supplies the final privacy-safe, idempotent v4
historical-case importer, batch/source ledgers, dry-run and rollback controls,
non-escalating history projection, and direct-command cutover validation.

QP-E's audit/statistics contracts and the separately owned appeal/honeypot
contracts are installed on the combined QI-2 runtime anchor.

## Optional Modules Must Stay Separate

Some v4 features remain part of Quack v5 as optional guild modules, not as extensions of the case model. Tickets and general logging now implement this boundary through the isolated module registry, lifecycle, Discord adapter, route, migration, and privacy contracts documented in `docs/modules/optional-tickets-and-logging.md`.

- Honeypots now invoke a configured template through an injected normal-case application boundary, with system attribution, message deduplication, drift disablement, isolated statistics/migration/runtime contracts, and no direct case or action storage access. QI-2 supplies the production system-only adapter, authoritative Discord projections/validators, central routes and frozen logical-0300 migration, drift forwarding, conditional intents, and bounded runtime lifecycle described in `docs/modules/optional-honeypots.md`.

Purge, lockdown, ping, server information, and similar utilities may be considered later. They should not add fields, permissions, or special cases to the core moderation system.

## Using This Audit

Future slices should begin with the relevant rule in `v5.md`. When a slice touches an item above, it should either bring the implementation closer to that rule or update the product definition first when the intended behavior has genuinely changed.

Remove items from this audit only after the code and its technical documentation agree with `v5.md`.
