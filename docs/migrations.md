# Database Migrations

Production startup applies an ordered migration registry from `apps/backend/internal/store`.
Each successful migration is recorded in `quack_schema_migrations` with its
version, name, checksum, and application time. Startup verifies every existing
ledger checksum before it runs new work. The checksum includes an embedded copy
of the migration's Go source, including its frozen schema records and `Up`/`Down`
logic, so executable edits cannot retain the old identity. There is no
production or development `AutoMigrate` path.

Migration definitions are additive by default and must preserve table names,
identifiers, guild case numbers, snapshots, action attempts, events, and audit
rows. The first migration adopts the current pre-ledger v5 schema or creates it
on a clean database. It never drops or renames an application table or column.

Migration 0002 separates the simplified live template model from frozen
compatibility storage. It preserves every template, level, action, case
snapshot, and audit row. Templates that used disabled flags, escalation
windows, legacy soft deletion, multiple actions, duplicate/default threshold
defects, action-level notifications, public execution controls, unsupported
actions, or settings that cannot be mapped safely are quarantined for
administrator review and archived when they were active. The migration records
only prior archive/deletion state and reasons in
`quack_v5_0002_template_compatibility`. Detail reads of quarantined templates
return an explicit compatibility-review-required conflict instead of projecting
invalid legacy levels or actions through the live v5 contract. Valid archived
templates remain readable. The reviewed inverse restores recorded timestamps
and removes only the migration-owned bookkeeping table.

Migration 0003 replaces mixed case lifecycle status and generic source labels
at the live boundary. It validates all rows before changing data, maps the
reviewed legacy values to `valid`/`voided` and
`dashboard`/`discord`/`honeypot`/`v4_import`, and fails explicitly on unknown
values. It preserves severity, weight, snapshots, actions, attempts, audit
history, and every case event in frozen storage. Retired note and generic
`status_changed` events are inventoried by case in
`quack_v5_0003_case_compatibility` and excluded from live event queries. Its
reviewed inverse restores exact prior status/source values and drops only that
migration-owned table. Cases created after migration 0003 are explicitly mapped
from canonical validity/source values back to the compatible legacy
`open`/`voided` and source labels before the ledger entry is removed.

Migration 0004 creates one `guild_settings` row per guild. It stores core audit
mirror and managed-evidence channel references, bounded notification
introduction/footer text, independent ticket/logging/honeypot enablement, the
starter-template identity, and one-time starter-review notice state. Existing
guilds receive conservative defaults without changing any guild, staff,
template, case, event, action, attempt, appeal, ticket, audit, identifier, or
history row. Its reviewed inverse drops only `guild_settings`; rerunning forward
re-seeds one row per guild. SQLite and isolated real-MySQL coverage exercise
forward, rerun, preservation, rollback, and reapplication.

Migration 0005 adds the core moderation runtime without rewriting existing
history. It creates ordered template-context definitions, immutable message and
attachment evidence, and exactly-one case notifications. Additive case columns
hold context, void/replacement links, and nullable idempotency keys. Action
columns add leases, fencing, dismissal, and original-execution/accepted-appeal
reversal links. Existing templates require no new context, existing cases are
backfilled with an empty context array, and existing actions remain claimable.
Its reviewed inverse drops only migration-owned tables and additive columns.

Migration 0006 places logical optional-module migration 0100 in the contiguous
production ledger. It creates only guild-scoped opaque module configuration and
the idempotent v4 import identity ledger. Migration 0007 similarly reconciles
logical ticket migration 0110: it preserves the baseline ticket and event rows
while adding separately retained transcripts and member abuse-control state.
Both definitions use frozen storage primitives and checksum-bound source.
Because rolling either migration back could discard operator configuration,
import identities, ticket transcripts, or ticket lifecycle state, 0006 and 0007
are explicitly forward-only. Core migration 0005 retains its reviewed inverse
when it is the newest applied migration before the forward-only module suffix.

Migration 0008 places logical honeypot migration 0300 in the contiguous ledger.
It creates isolated trigger/deduplication history without granting that module
direct case/action storage access. Migration 0009 places logical appeal
migration 0200 after the already-reviewed module prefix. It upgrades preserved
appeal rows into one case-linked, versioned timeline with guild settings and a
durable notification outbox. Both are checksum-bound, additive, resumable, and
forward-only because their module/appeal histories must not be deleted by a
binary rollback.

Migration 0010 places logical v4 import migration 0400 in the ledger. It adds
only privacy-safe batch and source identity ledgers; imported cases use normal
historical projections and never create action or notification work. Migration
0011 places logical final-storage migration 0410 last. It converts residual
legacy template deletion state to archive state, installs one-default and
one-action uniqueness plus final query/claim indexes, and inventories unsafe
expired running actions for manual review without changing their history. Both
are forward-only. Coexistence with v4 is documented in
`v4-historical-import.md`.

## Forward procedure

1. Back up MySQL and verify the backup before deploying schema-changing code.
2. Review the ordered migration definition, embedded source, `Up` operation,
   and, when safe, its idempotent `Down` operation in the pull request.
3. Stop additional Quack processes or leave them waiting on the MySQL migration
   lock. Run `quack migrate up` with the production
   `QUACK_DATABASE_DSN`, or start one new Quack process and let startup run the same
   method.
4. Verify the command succeeds and inspect `quack_schema_migrations`. Do not
   manually insert, delete, rename, or edit ledger rows.
5. Start the remaining processes and verify readiness before normal traffic.

Rerunning `up` is expected and safe. Applied migrations are checksum-verified
and skipped. A failed migration is not recorded as successful. MySQL may commit
DDL even when a later statement fails, so every `Up` operation must detect its
already-applied additive work and safely resume on the next run.

## Failure and recovery procedure

1. Keep the application unavailable when the new binary requires the failed
   schema transition.
2. Preserve the error and current ledger; never mark a failed migration applied
   by hand.
3. Inspect the database because MySQL DDL can survive a failed transaction.
4. Fix the migration so its reviewed `Up` operation resumes from that state,
   then rerun `quack migrate up`.
5. Restore the verified backup only when additive recovery cannot preserve the
   required records, and rehearse that restore before production use.

## Rollback procedure

Run `quack migrate down` only after reviewing the newest applied
migration and confirming its idempotent `Down` operation preserves v5 history.
Before executing MySQL DDL, the runner durably marks the ledger row
`rolling_back`. Normal `up` and application startup refuse that dirty state.
After `Down` succeeds, the runner removes the ledger row. If the process or DDL
fails before removal, rerun the same reviewed `down`; it resumes the idempotent
inverse and completes ledger cleanup. Never clear `rolling_back` by hand. Tests
cover partial-DDL recovery and reversible rollback on SQLite and MySQL.

Migrations without a safe inverse are explicitly forward-only. The initial v5
baseline is forward-only because reversing it would hard-delete moderation and
audit history; `down` returns `ErrMigrationNotReversible` and changes neither
the schema nor ledger. For an additive forward-only release, roll back the
application binary while retaining the compatible schema, then ship a new
forward migration for any database correction.

Every future schema change must live in a new source file embedded by its new
registry entry. Do not edit an applied migration. Its focused tests must cover
forward execution, rerun behavior, failure recovery, preservation of
representative data, and either an idempotent reviewed inverse with dirty-state
recovery or the explicit forward-only refusal boundary.
