# Quack v5 Rehearsal Protocol

This protocol records the release evidence that cannot be inferred from unit
tests. Product behavior remains defined by [`v5.md`](../v5.md). A skipped step
is recorded as **NOT EXECUTED**, together with its missing dependency; it is
never converted into a pass.

## Automated local gate

Run `apps/backend/scripts/v5-readiness.sh --local` for the isolated composition, focused,
race, repository-wide test, vet, and build gates. Run
`apps/backend/scripts/v5-readiness.sh --final` with disposable MySQL and Redis targets for
the external-storage gate. The final mode fails when either target is absent.

## Clean install and current-schema upgrade

Use disposable storage only.

1. Create an empty MySQL database and isolated Redis namespace.
2. Run `quack migrate up`; capture the command, commit, database
   identity, migration ledger, start/end timestamps, and exit status.
3. Start Quack with non-production Discord credentials, confirm liveness and
   readiness separately, then stop it through the documented graceful path.
4. Seed the last accepted pre-P3 schema with representative guild, template,
   valid/voided case, pending/running action, evidence, appeal, audit, and
   module rows. Back up row counts and immutable identifiers.
5. Run the final migration registry twice. Verify the second run is a no-op and
   all identifiers, guild case numbers, snapshots, action attempts, appeal
   history, audit rows, and module state are preserved.
6. Confirm unsafe or uncertain expired action claims enter manual review and
   are not executed merely because the process restarted.

## Backup and restore

1. Create a logical backup from the upgraded disposable MySQL database and
   record the tool/version, consistency options, checksum, and row counts.
2. Restore into a new empty database, point a new isolated Quack process at the
   restored database and a fresh Redis namespace, and rerun migrations.
3. Verify immutable identifiers, guild case-number maxima, action attempt and
   lease state, appeals, audit history, import ledgers, and module settings.
4. Start workers with a fake or controlled Discord adapter. Prove restoration
   does not duplicate case numbers, actions, notifications, appeal deliveries,
   or v4 import rows.
5. Exercise rollback only where the newest migration declares a reviewed safe
   inverse. For forward-only migrations, verify binary rollback preserves the
   schema and document the forward-correction procedure.

## V4 and v5 coexistence

1. Use separate schemas, Redis namespaces, Discord application credentials,
   command scopes, and process configuration.
2. Run the v4 importer in dry-run mode against sanitized representative rows;
   verify the report contains counts and non-sensitive warnings, then apply it
   twice and prove the second application is idempotent.
3. Verify imported history remains readable but creates no v5 escalation,
   action, notification, DM, or automatic reversal.
4. Compare v4 and v5 registered command names. Confirm direct punishment
   commands remain owned by v4 during coexistence and are absent from v5.
5. Rehearse rollback by stopping v5 and pruning only its scoped commands. Do
   not mutate production command scope or credentials during repository
   validation.

## Real-guild checklist

This section requires an explicitly authorized non-production Discord guild
and human observation.

- Install with the documented OAuth scopes, bot permissions, and intents.
- Verify starter policy, one-time notice, managed evidence channel ACL, repair,
  leave/rejoin preservation, and channel-reference drift handling.
- Exercise owner, Administrator, Manage Guild, Moderate Members, action-specific
  permission, former-staff, hierarchy, bot, self, and guild-owner boundaries.
- Create actionless, timeout, kick, and ban cases from HTTP and Discord; verify
  public/private response boundaries, evidence preservation, one DM, member
  access after departure, appeal entry, appeal decisions, explicit reversals,
  audit mirror, failure review, retry, dismissal, and voiding.
- Exercise tickets, general logging, and honeypots independently and confirm no
  module writes into another module or the core case model outside the normal
  honeypot case-application boundary.
- Cancel the process while actions, evidence, audit mirror, appeal delivery,
  and optional-module work are active; verify bounded shutdown and safe resume.

Record guild ID only if the evidence artifact is access-controlled. Never put
member content, tokens, cookies, webhook URLs, OAuth grants, or raw Discord
errors into readiness evidence.
