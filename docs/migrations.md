# Database Migrations

The schema lives in `apps/backend/internal/store`: `schema.go` has one record
struct per table, and `migrate.go` has the ordered list of migrations. Each
applied migration is recorded in `quack_schema_migrations` by version and name.
`quack migrate up` (or just `quack migrate`) and `quack serve` both apply
whatever is pending. `migrate` only needs `database.dsn`.

There is one migration today, version 1 `baseline`. It creates every table,
including the module tables (`modules.Models`, `tickets.Models`,
`honeypot.Models`), with GORM AutoMigrate and then adds the one constraint
struct tags cannot express: at most one default level per template. MySQL has
no partial indexes, so there it is a unique index on a generated
`default_template_id` column; SQLite (tests only) uses a partial unique index.

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
