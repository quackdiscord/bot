# v4 import

`quack import-v4` loads moderation history from Quack v4 into one v5 guild.
Imported cases are historical records: they show up in case history and
statistics, but they never count toward escalation and never create actions,
DMs, notifications, or appeal work. Only cases are imported. Tickets, logs,
honeypots, and other v4 data are out of scope.

The code is in `apps/backend/internal/v4import` (parsing and validation) and
`apps/backend/internal/store/v4import.go` (writes and rollback). The import
needs only `database.dsn`; it does not connect to Redis or Discord.

## Input format

The input is JSONL: one object per line, format `quack-v4-case-jsonl/v1`.
Unknown fields are rejected.

| Field | Required | Notes |
| --- | --- | --- |
| `format` | yes | Must be `quack-v4-case-jsonl/v1`. |
| `source_id` | yes | Stable ID of the v4 record, at most 191 characters. |
| `guild_id` | yes | The v5 guild ULID. Every row must match `-guild`. |
| `target_discord_user_id` | yes | The member the case was against. |
| `reason` | yes | The v4 reason. |
| `action_type` | yes | `warning`, `timeout`, `kick`, or `ban`. |
| `created_at` | yes | RFC 3339 timestamp. |
| `case_number` | no | The v4 case number. Kept when free in the guild, otherwise remapped. |
| `moderator_discord_user_id`, `moderator_display_name` | no | Who acted in v4. |
| `context_url` | no | A Discord link for context. |
| `target_departed`, `target_missing` | no | The member has left, or is unknown. |
| `action_expires_at` | no | When the v4 action expired. |

See `apps/backend/internal/v4import/testdata/historical_cases.jsonl` for
examples. A file may be at most 64 MiB. Nothing in this repository produces
the export; it has to be written from the v4 database separately.

The guild must already exist in v5, which means the v5 bot has joined it. Its
ULID is `guilds.id` for the row whose `discord_guild_id` is the Discord server
ID.

## Commands

All commands take `-config <file>` like the rest of the binary. Flags go
before arguments.

```sh
# Validate and preview without writing anything.
quack import-v4 import -dry-run -file guild.jsonl -source v4-final \
  -guild 01J... -actor 123456789012345678

# Import for real.
quack import-v4 import -file guild.jsonl -source v4-final \
  -guild 01J... -actor 123456789012345678

# Undo one batch.
quack import-v4 rollback -guild 01J... -batch v4-... -actor 123456789012345678
```

- `-file`, `-source`, `-guild`, and `-actor` are required for `import`.
  `-source` is a stable name for the export, and `-actor` is the operator's
  Discord user ID, used in the audit log.
- `import` prints a JSON report to stdout, even when it fails. The report has
  the batch ID, the file's SHA-256 checksum, counts, per-line decisions, and
  warning and failure codes. It never echoes reasons or member IDs.
- The batch ID is derived from the guild, source name, and checksum, so
  running the same file again gives the same batch. Rows already imported are
  reported as `AlreadyImported` and nothing new is written.

## What the importer guarantees

- **All or nothing.** Any malformed row (bad JSON, wrong format, missing
  field, wrong guild) fails the whole file before anything is written. A real
  run also records the failure in the audit log, by code and count only.
- **Collisions.** Reusing a `source_id` with different content is a hard
  error (`ErrSourceCollision`).
- **Warnings.** These are reported per line but do not stop the import:
  `case_number_remapped` (the v4 number was taken), `moderator_identity_unavailable`,
  `target_departed`, `target_missing`, and `expired_action_manual_review`.
  Expired actions are never replayed.
- **Storage.** Imported cases have source `v4_import`. They are tracked in
  `v4_import_batches` and `v4_import_sources`, and each batch is audited as
  `v4_import.batch`.

## Rollback

`rollback` deletes the batch's cases, their events, and its ledger rows, then
writes a `v4_import.rollback` audit entry. It refuses if any of those cases has
since gained rows in `case_action_executions`, `case_notifications`,
`appeals`, or `case_evidence_snapshots`. Audit history is never removed.

## Running v4 and v5 side by side

During a transition, run v4 and v5 as separate bots with separate databases,
Redis, and Discord applications. Check that their command names do not
collide:

```sh
quack import-v4 check-scope -v4 ticket,purge -v5 case
```

At cutover, remove v4's direct moderation commands. This check fails while any
of `warn`, `timeout`, `kick`, or `ban` is still in the v4 list:

```sh
quack import-v4 check-scope -v4 warn,timeout,kick,ban -v5 case -after-migration
```

`check-scope` needs no config or database.
