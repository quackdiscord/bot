# Work Queue

`apps/backend/internal/workqueue` is an injected in-process worker pool for case-action
execution. It contains no datastore or configuration globals.

## Source of truth

The queue channel is a wake-up mechanism, not durable storage. Pending and
retrying work lives in `case_action_executions`.

The scheduler:

- accepts immediate case-ID hints after case creation;
- discovers executable cases at startup and every second;
- reads at most 100 case IDs per discovery pass;
- lets database claim locks prevent duplicate execution;
- records accepted, dropped, processed, and failed hint counters.

A full channel may drop an immediate hint without losing the action because the
next discovery pass reads it from MySQL.

## Retries

Action completion writes `next_retry_at`. No delayed goroutine owns retry
state. Once the timestamp becomes due, normal discovery submits the case again.
This makes delayed retry behavior recover after process restarts.

## Lifecycle

`apps/backend/internal/runtime` constructs the queue from `queue.size` and
`queue.workers`, injects it into the application services, and starts it
with the action processor and repository due-work source.

Shutdown marks the queue inactive, cancels polling, closes the job channel
outside the queue mutex, and waits for polling and workers to drain. New
submissions are rejected after shutdown begins.

## Limits

- Workers still run in the API/bot process.
- There is no dead-letter or manual replay interface yet.
- A repeatedly failing queue handler is logged and rediscovered from persisted
  state according to its execution status.

Relevant files:

- `apps/backend/internal/workqueue/queue.go`
- `apps/backend/internal/quack/actions.go`
- `apps/backend/internal/store/cases.go`
- `apps/backend/internal/runtime/runtime.go`
