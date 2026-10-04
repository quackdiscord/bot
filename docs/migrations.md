# Database Migrations

The schema lives in `apps/backend/internal/store`: `schema.go` has one record
struct per table, and `migrate.go` has the ordered list of migrations. Each
applied migration is recorded in `quack_schema_migrations` by version and name.
`quack migrate up` (or just `quack migrate`) and `quack serve` both apply
whatever is pending. `migrate` only needs `database.dsn`.

There are two migrations today.

Version 1 `baseline` creates every table,
including the module tables (`modules.Models`, `tickets.Models`,
`honeypot.Models`), with GORM AutoMigrate and then adds the one constraint
struct tags cannot express: at most one default level per template. MySQL has
no partial indexes, so there it is a unique index on a generated
`default_template_id` column; SQLite (tests only) uses a partial unique index.

The schema is unreleased, so the baseline is edited in place. A database that
recorded an older baseline is brought up to date on every migrate: re-running
AutoMigrate adds missing tables and columns, and `retireAppealForms` first
removes what custom appeal forms left behind (`guild_appeal_settings`,
`appeals.question_snapshot_json` and `answers_json`, with `appeals.content`
renamed to `statement` and filled from the old answers).

Version 2 `audit_mirror_deliveries` creates the audit mirror's queue table,
which the baseline also creates on a fresh database. Until then the mirror
recorded each delivery outcome as another audit entry and found pending work
by scanning the audit log. Nothing is backfilled: important entries written
before the migration get no delivery row, so none can be posted twice. The
cost is that entries still waiting at upgrade time (written in the last few
seconds before shutdown, or stuck failing) are not mirrored; their audit
history is unaffected. The step has no `down`: the older binary would see no
delivery outcomes for entries mirrored since, and post them all again. So
`quack migrate down` now stops at version 2, `-drop-all` included.

On MySQL, migrators take a named lock (`GET_LOCK`), so several processes can
start at once and run migrations one at a time.

## Adding a migration

Append a `migration{version, name, up, down}` with the next version. Never edit
or reorder one that has shipped; the ledger checks versions and names and
refuses to run against a database it does not recognize. MySQL commits DDL
immediately, so an `up` that fails halfway is rerun from the start: write it to
detect work it already did. Leave `down` nil when the step cannot be undone
safely.

## Rolling back

`quack migrate down` undoes the newest applied migration with its `down` step,
or fails when it has none. Undoing the baseline drops every table and all data,
so it is refused unless you pass `-drop-all`:

```
quack migrate -drop-all down
```

## Forward procedure

1. Back up MySQL and verify the backup before deploying schema changes.
2. Run `quack migrate up` with the production `QUACK_DATABASE_DSN`, or start
   one Quack process and let startup migrate.
3. Check that it succeeded and inspect `quack_schema_migrations`. Do not edit
   ledger rows by hand.
4. Start the remaining processes and check readiness (`/readyz` reports the
   schema version) before sending traffic.

## Failure and recovery

1. Keep the new binary out of service while its schema is incomplete.
2. Keep the error and the ledger as they are; never mark a migration applied by
   hand.
3. Inspect the database, since MySQL DDL survives a failed migration.
4. Fix the migration so it resumes from that state, then rerun
   `quack migrate up`.
5. Restore the verified backup only when the migration cannot be resumed.
