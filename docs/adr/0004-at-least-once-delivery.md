# ADR 0004: At-least-once delivery, exactly-once logical transitions

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [communication.md](../communication.md), [failure-and-recovery.md](../failure-and-recovery.md)

## Context

Distributed delivery is at-least-once in practice: a sender cannot distinguish a lost message from a lost
acknowledgement. Promising exactly-once delivery across PostgreSQL, NATS and HTTP services would either be false or
require a distributed transaction. The product needs a precise, defensible guarantee: a task must not complete
twice, an execution must not transition twice, and a client retry must not create a second execution. It does not
need every message delivered once.

## Decision

- **Transport may duplicate; logical transitions may not.**
- The system provides **at-least-once delivery** for dispatch and events, and **exactly-once logical state
  transitions** through guarded writes.
- Mechanisms by layer:
  - **API submission:** `Idempotency-Key` + unique `(org_id, request_id)` on executions. Same key/body returns the
    original execution.
  - **Task dispatch:** each assignment creates a unique attempt; dispatch carries `attempt_id`. Worker accepts only
    the currently open attempt under its lease; stale/duplicate dispatch is dropped.
  - **Result reporting:** fenced by `(task_id, attempt_id, lease_id, state, unexpired lease)`. The first valid
    result commits; later duplicates return `ALREADY_FINISHED`/`STALE_LEASE`.
  - **State transitions:** conditional `UPDATE ... WHERE state = expected AND state_version = expected_version`
    (or a row lock), with the audit event and outbox row in the same transaction.
  - **Events:** per-execution monotonic `seq`; consumers dedupe by `seq`. Events never drive state.
  - **Outbox publishing:** a published-but-unmarked row may be published again; the receiver's fence makes it
    harmless.
- Exactly-once **external** side effects are explicitly out of scope; they are addressed by ADR 0006.
- No component may claim "message seen" as its only idempotency mechanism for state.

## Consequences

**Positive**

- The guarantee is honest and testable under injected duplicates, crashes and retries.
- Workers and consumers can be simple: do the work, report, tolerate a repeat.
- No distributed transaction or consensus protocol is required for correctness.

**Negative**

- Every consumer must be designed for duplicates; this is a permanent review obligation.
- A task can execute twice during a failover (old worker not yet dead). The database commits only one, but external
  effects may repeat unless declared idempotent.
- Broker duplicate windows are an optimisation, not a safety net; tests must delete ack state deliberately.

## Alternatives considered

- **Exactly-once delivery via broker transactions.** Does not cover HTTP APIs or external tools, and still leaves
  the worker crash window. Rejected as misleading.
- **Deduplication table for every message.** More state and cleanup for the same outcome the fence already
  provides. Use only where no natural key exists (API idempotency keys).
- **Synchronous request/response for dispatch instead of a queue.** Couples worker availability to scheduling and
  loses durability; does not solve duplicate results either.

## Verification

- Fault-injection tests duplicate every message at every hop and assert one logical transition.
- Tests retry `POST /executions` with the same key and assert one row.
- Tests deliver an old attempt result after reassignment and assert `STALE_LEASE`, no state change.
- The event consumer dedupes by `seq`; a replayed SSE stream produces no duplicate applied state.
