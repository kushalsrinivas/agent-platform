# ADR 0015: Workflow replay semantics

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [architecture.md](../architecture.md), [data-ownership.md](../data-ownership.md), [ADR 0002](0002-postgres-source-of-truth.md), [ADR 0008](0008-checkpoint-serialization.md)

## Context

"Replay" means different things:

1. reconstructing execution state from an event log, Temporal-style, to resume exactly where it stopped;
2. re-running a completed workflow from the beginning with the same inputs;
3. resuming a failed task from a checkpoint;
4. debugging by reading the event timeline.

Temporal-style replay requires a totally ordered command history, deterministic workflow code and deterministic
side-effect handling. Our workflow is a DAG with LLM nodes and external tools; LLM calls are non-deterministic,
tool results are external, and events are concurrent across tasks. Building a deterministic replay engine would be
a second product and would make every node's side effects a replay hazard.

## Decision

- **No event-sourced replay of workflow state.** `execution_events` is an audit/fan-out log, not a command log.
  State lives in rows (ADR 0002); events are never replayed to reconstruct or advance an execution.
- **"Replay" in the product means starting a new execution** with a new `execution_id`, the same pinned workflow
  version and the same input (or an edited copy). The old execution remains immutable. A future "fork" feature must
  still create a new execution and record lineage; it may not mutate the old one.
- **Task retry is the only resume path.** It creates a new attempt, optionally from the latest committed checkpoint
  (ADR 0008). It never replays the event log and never reuses an attempt.
- **Determinism is not required for correctness.** A node may produce different output on a retry; that is a
  product property to document, not a platform invariant. Idempotency declarations (ADR 0006) govern side effects.
- **Debugging uses committed state + events + traces.** The dashboard shows the timeline; the API exposes events by
  `seq`. No "time travel" mutation is exposed.
- If a future feature needs deterministic replay, it must:
  - introduce an explicit record/replay contract for model and tool calls (prompt/response capture);
  - define a deterministic scheduler for node execution;
  - add a new execution mode/ADR and storage policy for recorded I/O.
  Until then, no code may assume events can rebuild state.

## Consequences

**Positive**

- The control plane stays small: no replay runtime, no determinism constraints on Python/node code.
- Any workflow version can be re-run; debugging is straightforward from state and audit.
- LLM non-determinism is acknowledged rather than fought.
- Event retention can be bounded without breaking recovery (ADR 0013).

**Negative**

- Cannot reconstruct the exact mid-run memory or "rewind" a live execution.
- Re-running a workflow repeats external effects unless nodes are idempotent; this must be visible to users.
- Comparing two runs is a product feature, not a free property of replay.
- Debugging a non-reproducible failure relies on captured outputs/traces, not deterministic re-execution.

## Alternatives considered

- **Full event sourcing/replay (Temporal-like).** Powerful, but requires deterministic code, recorded side effects
  and a command log; large complexity for uncertain value. Rejected for Phase 1; could be layered later for
  specific node types.
- **Snapshot the whole worker/runtime and restore.** Python process snapshots are fragile across machines and
  libraries; not viable.
- **Reuse an execution ID for a retry.** Violates audit and immutable history; breaks idempotency and metrics.
- **Make checkpoints the replay log.** Checkpoints are partial, runtime-specific and not a total order; they resume
  tasks only.
- **Record every LLM/tool call and replay verbatim.** Useful for debugging and testing, but expensive, privacy
  sensitive, and still does not make DAG scheduling deterministic. A future opt-in feature, not the default.

## Verification

- A code-review/architecture test forbids reconstructing state from `execution_events`.
- API has no endpoint to resume/replay an existing execution; only cancel and create.
- Retry tests prove a new attempt, new attempt ID, same execution/task, optional checkpoint resume.
- Re-run tests create a new execution with a new ID and preserve the old rows unchanged.
- Debugging docs and UI labels say "start a new run", never "replay this execution".
