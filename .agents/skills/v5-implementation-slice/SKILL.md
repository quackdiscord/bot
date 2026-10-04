---
name: v5-implementation-slice
description: Implement one bounded Quack v5 work package, which may absorb several legacy execution-plan slice IDs, through one PR, one Codex review request, fixes, proportionate revalidation, and evidence handoff. Use only when the orchestrator assigns concrete acceptance criteria, an exclusive write set, a branch/worktree, and a base anchor.
---

# Quack v5 implementation work package

Own one assigned work package end to end. Complete the capability; do not split
it into smaller PRs merely to reduce local complexity.

## Contract

Before editing, confirm:

- absorbed V5 requirement/slice IDs and acceptance criteria;
- dependency anchor, branch, worktree, and PR base;
- exclusive write set and integration-owned files you must not edit;
- focused checks and applicable schema/MySQL/race/external-adapter gates.

Read only the relevant parts of `v5.md`, scope drift, TODO, the execution plan,
and adjacent code. Derive missing engineering details locally. Escalate only a
genuine product ambiguity or contract collision.

## Throughput rules

- Work only in the assigned worktree and branch.
- Implement the entire package before opening a PR.
- Prefer one implementation commit and one review-fix commit.
- Run focused tests while developing; do not repeatedly run the full repository
  suite after every edit.
- Update TODO/docs once near package completion.
- Avoid integration-owned central wiring. Expose registrars/adapters for the
  integration checkpoint instead.
- Send checkpoints only for a product decision, blocker, review-ready head,
  Codex findings, or final handoff.
- Do not merge, force-push, or expand into another package.

## Implementation and validation

1. Inspect the existing contracts and tests once.
2. Implement all assigned acceptance criteria and compatibility behavior.
3. Add focused contract, boundary, and regression tests.
4. Run focused checks until green.
5. On the reviewable head, run one required repository-wide test/vet/build pass.
6. Run MySQL only when the package changes schema/migrations; run race only for
   concurrency/shared-state work.
7. Run gofmt on changed Go files and `git diff --check`.
8. Verify the diff stays inside the assigned package and documented shared
   contract exceptions.

## PR and one-review lifecycle

Commit and push. Open one non-draft PR containing requirement IDs, acceptance
coverage, summary, exact validation, decisions, limitations, and discoveries.
Post one separate comment containing exactly:

```text
@codex review
```

Poll structured PR state about every five minutes. Query comments, submitted
reviews, inline threads, checks, and head OID together. An EYES reaction is not
a completed review. Do not ask the orchestrator to duplicate polling.

For each finding record its reference/severity, validity, scope, disposition,
and supporting reason. Fix valid in-scope findings. Record valid out-of-package
work in TODO/plan evidence without implementing it.

After review fixes:

- run tests targeted to the finding;
- repeat the full suite only if the fix changes schema, shared contracts,
  architecture, or broad behavior;
- commit and push fixes;
- do not request another Codex review by default.

A second review requires orchestrator approval and architecture-scale rework.

If an external Git/GitHub action is policy-rejected, do not retry. Report the
exact mechanical command or PR/comment payload once so the orchestrator can
perform only that step when authorized.

## Handoff

Return after the lifecycle completes, or when mechanically blocked, with:

- package and absorbed IDs;
- branch/worktree/base/PR and final head;
- acceptance-criteria evidence;
- focused and applicable full/MySQL/race/build results;
- review request count, reviewed commit, findings, fixes, and thread state;
- files/contracts changed and integration instructions;
- TODO/docs updates and discovered work;
- `READY_FOR_ORCHESTRATOR_VALIDATION` or exact blocker.
