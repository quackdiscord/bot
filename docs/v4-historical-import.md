# V4 Historical Moderation Import

Quack v5 accepts one final newline-delimited JSON format: `quack-v4-case-jsonl/v1`.
Every non-empty line is one historical warning, timeout, kick, or ban. Core import
does not map tickets, general logging, honeypots, or unfinished v4 modules; each
optional module owns its separate migration hook.

Required fields are `format`, `source_id`, `guild_id`,
`target_discord_user_id`, `reason`, `action_type`, and `created_at`.
`action_type` is one of `warning`, `timeout`, `kick`, or `ban`. Optional fields
preserve the v4 case number, moderator identity/display fallback, Discord context
URL, departed/missing target state, and an old action expiry. See
`apps/backend/internal/v4import/testdata/historical_cases.jsonl` for representative rows.

## Safe import procedure

1. Export one guild from v4 without changing the v4 database. Store it in an
   operator-only location and calculate an independent checksum.
2. Apply the v5 migration registry including logical 0400 and 0410.
3. Run a dry-run against an isolated restored v5 target:

   ```sh
   QUACK_DATABASE_DSN='operator supplied isolated DSN' quack import-v4 import \
     -dry-run -file ./guild.jsonl -source final-v4-export \
     -guild 01J... -actor 123...
   ```

4. Review only the checksum, counts, line numbers, and warning/failure codes.
   Do not paste reasons, member IDs, or source rows into logs or tickets.
5. Run the same command without `-dry-run`. Preserve its batch ID and report.
6. Repeat the command. It must report every row as already imported and create
   no cases.
7. Verify staff history and a target-owned member history view. Imported cases
   use source `v4_import`, carry their legacy identity in immutable metadata,
   and never create action executions, escalation counts, DMs, notifications,
   or appeal work.

Malformed rows abort the entire source before writes. Reusing a source identity
with changed content is a hard collision. A conflicting case number is remapped
to the next guild number and reported while the v4 number remains in metadata
and the source ledger. Departed/missing users and unavailable moderators remain
readable without requiring a live Discord lookup. Expired legacy actions are
warnings for manual review; they are never replayed.

An untouched batch can be removed with:

```sh
QUACK_DATABASE_DSN='operator supplied isolated DSN' quack import-v4 rollback \
  -guild 01J... -batch v4-... -actor 123...
```

Rollback refuses a batch after any v5 action, notification, appeal, or evidence
depends on an imported case. The audit history is retained.

## Controlled coexistence and cutover

V4 and v5 use separate databases, Redis namespaces, processes, application IDs,
and Discord command scopes during rehearsal. Before enabling both, compare the
registered names:

```sh
quack import-v4 check-scope -v4 ticket -v5 case
```

At cutover, disable v4 command synchronization and remove `/warn`, `/timeout`,
`/kick`, and `/ban`; do not retain them as alternate v5 workflows. The
post-migration check fails while any direct command remains:

```sh
quack import-v4 check-scope \
  -v4 warn,timeout,kick,ban -v5 case -after-migration
```

Rollback means disabling v5 command sync and re-enabling the isolated v4
process from its unchanged storage. Never point one version at the other's
schema or Redis prefix. Core rollback does not undo module-owned imports.

## Final integration contract

Add `migration0400V4HistoricalImport(nextVersion)` followed by
`migration0410FinalStorageConstraints(nextVersion)` to the central ordered
registry. Wire neither importer into application startup: import is an explicit
operator command. Logical 0410 converts any residual legacy template
`deleted_at` value into `archived_at`, clears it, adds final uniqueness/index
coverage, and inventories unsafe expired running actions for manual review.
