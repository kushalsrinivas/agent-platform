# ADR 0014: Cancellation semantics

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [state-machines/execution.md](../state-machines/execution.md), [state-machines/task.md](../state-machines/task.md)

## Context

Cancellation looks simple ("stop the run") but has several hard questions:

- Does a cancel always win, even if the work finishes at the same time?
- Can a worker be trusted to stop immediately? What if it is partitioned?
- What happens to a tool call already in progress?
- Is cancel idempotent? Can it be undone?
- What is the final state when a deadline is hit?

Without a decision, implementations diverge: some mark `cancelled` while tasks still run, some let success win
non-deterministically, some wait forever for an unreachable worker.

## Decision

### Sticky and explicit

- Cancellation is a state transition to `cancel_requested` from `pending` or `running`, recording
  `cancel_reason ∈ {user, deadline}` and incrementing `cancel_epoch` exactly once.
- Once requested, the execution can never end `succeeded`. It ends `cancelled` when all tasks are terminal, after
  which tasks that had succeeded keep their success.
- If the execution already entered `failing` (an unrecoverable task failure), it ends `failed`; a user cancel is a
  no-op and returns `409`. Failure propagation already performs the same task cancellation.
- Cancellation is not reversible. A new run is a new execution.
- The API request is idempotent while `pending`, `running`, `cancel_requested` or `cancelled`; `202` with current
  state. Terminal `succeeded`/`failed` return `409`.
- `cancel_epoch` lets workers ignore stale cancel messages; duplicate cancels do not increment it.

### Best effort, bounded time

- Scheduling stops immediately. Pending tasks become `cancelled` with no attempt.
- Active attempts receive a `CancelTask` over the worker control stream. The worker kills the sandbox: `SIGTERM`,
  5s grace, `SIGKILL` process group. Heartbeats stop renewing the leases, so cancellation is bounded even if the
  worker ignores the command or cannot be reached.
- If the worker is unreachable, the reaper waits for lease expiry (≤30s), closes the attempt as `lease_expired`
  (`outcome_unknown=true`), marks the task `cancelled`, and sets `effect_uncertain` if it had started. The
  execution can then finalise.
- A worker that reports `CANCELLED` without a matching cancel request/task cancellation is treated as a failure,
  not as a legitimate cancel (it cannot abandon work and call it cancellation).
- Results racing a cancel: the state machine serialises on the row. If the result commits first, the task succeeds
  and the execution still ends `cancelled`; if the cancel commits first, the result is rejected by the fence/state.

### Deadline

- An execution `deadline_at` is an automatic cancellation with `cancel_reason=deadline`.
- Its terminal state is **`failed`** with `failure_code=EXECUTION_TIMEOUT`, not `cancelled`, because no human asked
  to stop a healthy run; it ran out of time.
- If the execution was already user-cancelling, the user reason remains and it ends `cancelled`. If already
  `failing`, it ends `failed` via the failure path.
- Task deadlines are separate: the worker and reaper enforce them; a task timeout is a failure that may retry,
  while the execution deadline is absolute.

### No cascades, no rollback

- Executions do not spawn child executions in Phase 1; cancellation does not cascade. If child executions are added,
  cancellation must be modelled explicitly by the parent workflow and recorded, not implied.
- Cancellation never rolls back external side effects, checkpoints or partial results. Compensation is a workflow
  concern (ADR 0006).

## Consequences

**Positive**

- Deterministic final states: cancel wins over success, failure wins over cancel if it happened first.
- Cancellation always terminates, even with a dead worker or broker outage.
- Operators can see `effect_uncertain` rather than assuming a clean stop.
- The API contract is idempotent and safe to retry.

**Negative**

- A task may still execute briefly after cancel (network latency, hung process) and may produce effects.
- Users cannot "cancel and keep success" or reopen a cancelled run; they start a new execution.
- The distinction between `cancelled` and deadline `failed` needs explanation in the UI.
- Reaching a terminal state can take up to a lease TTL when a worker is unreachable.

## Alternatives considered

- **Success wins if it races the cancel.** Non-deterministic; users see different results for the same timing.
  Rejected.
- **Mark the execution cancelled immediately and let tasks finish in the background.** Violates "terminal means
  quiescent" and hides running work. Rejected.
- **Wait indefinitely for worker acknowledgement.** A crashed worker would hang cancellation forever.
- **Cooperative cancellation only.** Tenant code could ignore it; the worker must kill the process.
- **Rollback/compensation on cancel.** The platform cannot undo arbitrary external APIs; pretending otherwise is
  dangerous.
- **Deadline → `cancelled`.** Loses the operational distinction between "user stopped" and "ran too long".

## Verification

- State tests: cancel from pending/running, duplicate cancel, cancel after terminal, cancel during failing,
  deadline during each state.
- Race tests: result vs cancel, cancel vs execution completion, reaper expiry vs worker cancel report; exactly one
  final state, deterministically explained.
- Unreachable worker test finalises within lease TTL with `effect_uncertain`.
- API tests prove idempotent 202 and 409 on terminal success/failure.
- Worker tests prove SIGTERM→SIGKILL and no orphan process.
