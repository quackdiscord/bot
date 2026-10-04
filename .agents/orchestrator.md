# Quack v5 orchestrator

Read `v5.md`, `docs/v5-scope-drift.md`, `TODO.md`, and
`docs/exec-plans/active/v5-readiness.md`. Product precedence is `v5.md`, then
documented clarifications, then `TODO.md`. Treat the backlog as inventory, not
an exhaustive specification.

## Throughput objective

Finish complete product capabilities, not one checklist item per PR. A work
package may absorb several legacy V5 slice IDs when they share contracts and
write sets. Optimize for elapsed time and integration quality:

- keep up to three implementation subagents active in parallel;
- target no more than nine remaining implementation PRs after V5-003;
- assign one fresh subagent to each work package and never reuse it later;
- prefer one implementation commit and at most one review-fix commit;
- avoid serial review waits when another independent package can run;
- batch plan bookkeeping at assignment, material decision/blocker, review
  result, acceptance, and wave integration—not every poll or minor checkpoint.

Do not shrink product scope to meet these targets. Regroup work when a package
is too small, overly serial, or repeats validation/review overhead.

## Work-package design

Before a parallel wave, define for every package:

- absorbed requirement/slice IDs and acceptance criteria;
- dependency anchor and PR base;
- exclusive write set and shared contracts;
- files reserved for integration (normally central wiring, router/runtime
  registries, and migration ordering);
- focused checks and whether schema, race, MySQL, or external-adapter validation
  applies.

Parallel packages must not materially overlap. Package code should expose
registrars/adapters instead of editing integration-owned central files. Reserve
migration number ranges or reconcile registration only at integration.

Each package uses its own branch, isolated worktree, fresh subagent, and PR.
Subagents do not merge PRs.

## Parallel integration

Parallel PRs target the same accepted wave anchor. After their review-and-fix
lifecycles, create an integration branch from that anchor and locally combine
the accepted branch heads. This does not merge the GitHub PRs.

- If branches combine cleanly, run integration gates and use the resulting head
  as the next wave anchor; no extra review-only PR is required.
- If conflict resolution or integration requires behavior changes, assign a
  fresh bounded integration subagent, test the changes, and use one PR/Codex
  lifecycle for that implementation.
- Never begin a dependent wave from mutually unintegrated heads.

## Implementation lifecycle

For each work package:

1. Give the fresh owner the authoritative acceptance contract, dependency
   anchor, exclusive write set, integration-owned exclusions, and validation
   tiers.
2. The owner implements the complete package, updates tests/docs/backlog once,
   runs focused checks while developing, then runs the required pre-review gates.
3. The owner commits, pushes, opens one non-draft PR, and posts exactly one
   standalone `@codex review` comment.
4. The owner polls structured GitHub state about every five minutes, triages all
   findings, applies valid in-scope fixes, and pushes them without a second
   review request by default.
5. A second request is allowed only for exceptional architecture-scale rework
   and requires orchestrator approval and a documented reason.
6. The owner returns a structured evidence handoff.

While reviews are pending, schedule other independent packages. Do not duplicate
the owner's GitHub polling. If an external write is rejected, record the exact
mechanical payload once; the orchestrator may perform only that mechanical step
when authorized.

## Validation economy

Match validation to risk without repeating it mechanically:

- focused tests during development;
- one repository-wide test/vet/build pass on the reviewable package head;
- MySQL migration tests only for schema-producing packages;
- race tests only for concurrency/shared-state packages;
- post-review targeted checks for narrow fixes;
- repeat the full suite after a review fix only when it changes schema, shared
  contracts, architecture, or broad behavior;
- run repository-wide integration gates once per combined wave.

## Orchestrator acceptance

Post-Codex acceptance is an evidence gate, not a second code review. Verify the
handoff accounts for acceptance criteria, scope boundaries, required passing
commands, PR/head state, the single review request, and every finding's
disposition. If tests pass and Codex reports no bugs, accept immediately. If a
finding was fixed, inspect or rerun only the affected evidence unless the fix is
broad. Do not routinely re-read the whole diff or rerun the full suite.

Return incomplete work to its owner; never silently implement fixes in the
orchestration worktree. Cosmetic unresolved GitHub threads do not block when the
underlying finding is fixed and evidenced. Unresolved actionable findings do.

## Plan and evidence

Keep the execution plan authoritative but concise. Record package ownership,
branches/PRs, absorbed IDs, decisions, findings, validation, blockers, wave
integration heads, and discoveries. Update `TODO.md` and scope drift in the
owning package, checking only proven work. Preserve detailed validation in PR
bodies/handoffs; summarize rather than duplicate it in the plan.

## Completion and prohibited actions

Final readiness still requires every `v5.md` requirement, applicable TODO and
scope-drift item, repository-wide gate, migration/E2E/rehearsal, and
`docs/v5-readiness.md` evidence matrix to be complete or explicitly adjudicated.

Use `gh` for PR/review operations. Never merge GitHub pull requests,
force-push, delete branches, change repository settings, or modify release
infrastructure without explicit user authorization.
