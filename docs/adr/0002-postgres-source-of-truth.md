# ADR 0002: PostgreSQL as the source of truth

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [data-ownership.md](../data-ownership.md), [state-machines/execution.md](../state-machines/execution.md)

## Context

Execution state, task ownership and event history must survive process crashes, broker loss and duplicate
messages. We need transactional transitions, conditional ownership, unique constraints for idempotency, and a way
to recover after restart. Candidates included an event store, Redis, an embedded database per service, and
PostgreSQL.

The system's core operation is a small, guarded row update plus an audit append, under concurrency from workers,
reapers and API requests. This is a relational workload, not an analytics workload.

## Decision

- **One PostgreSQL cluster is authoritative** for control definitions and execution state. Schemas `control` and
  `execution` have separate owners and roles.
- A logical transition commits `state` + related rows + `execution_events` + outbox in **one transaction**. If it
  does not commit, it did not happen.
- Leases, deadlines and expiry use the **database clock** (`now()`), never worker clocks.
- Ownership is enforced by conditional updates and row locks. Uniqueness invariants (request ID, attempt number,
  open attempt, event sequence) are database constraints, not application etiquette.
- Event history is an audit/fan-out log, not an event-sourcing source of truth (ADR 0015). State rows are read
  directly.
- Object storage holds blobs (checkpoints, artifacts, logs); the database holds references, hashes and metadata.
- Read replicas may serve dashboards later but never accept writes. Phase 1 reads the primary.

## Consequences

**Positive**

- Strong, well-understood correctness model: ACID transitions, unique indexes, foreign keys, row locks.
- Recovery is re-derivation from rows; no replay engine or projection rebuild required.
- One place to enforce tenant filters, retention and audit.
- Familiar operational tooling: backups, PITR, migrations, monitoring.

**Negative**

- PostgreSQL is a write-availability dependency; a database outage stops new transitions.
- Per-execution event sequencing serialises appends for that execution. Acceptable at Phase 1 scale; if a single
  execution has thousands of concurrent task events, revisit with a separate allocator or accept gaps in `seq`.
- Schema migrations require expand/contract discipline across two deployables.
- A shared cluster is a noisy-neighbour risk; large tenants or heavy queries need limits/monitoring.

**Operational**

- Production requires HA (synchronous standby or a managed equivalent) before claiming RPO 0.
- Backups, PITR drills and connection-pool limits are part of the deployment contract.
- RLS plus service-level tenant filters are defense in depth; the orchestrator's cross-tenant scheduler role is the
  documented exception.

## Alternatives considered

- **Event store as the only truth (event sourcing).** Elegant replay story, but every query becomes a projection,
  schema evolution is harder, and the team would own a replay runtime. Rejected for Phase 1 (ADR 0015).
- **Redis as the ownership/lease store.** Fast, but durability and transaction semantics are weaker, and split
  brain recovery is harder. Redis may later be a cache, never the authority.
- **Database per service.** Cleaner isolation but cross-service transactions/sagas for assignment/result would
  add complexity without a current scaling need. The schema boundary keeps this option open.
- **Embedded SQLite per orchestrator.** Cannot support multiple replicas or durable shared state. Rejected.

## Verification

- Tests kill the orchestrator between commit and publish/response and prove state recovery from rows.
- Unique constraints reject duplicate submissions, duplicate open attempts and duplicate event sequences.
- Lease tests use the database clock and prove worker clock skew does not extend a lease.
- A restore drill shows non-terminal executions recover via the reaper, not via in-memory state.
