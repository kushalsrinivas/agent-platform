# Architecture Decision Records

ADR numbering is permanent. An accepted ADR is never edited to reverse its decision; a later ADR supersedes it.
Phase 01 decisions are all `Accepted` as of 2026-11-22.

## Index

| ADR | Decision | Status |
|---|---|---|
| [0001](0001-go-control-plane-python-runtime.md) | Go control plane, Python agent/tool runtime | Accepted |
| [0002](0002-postgres-source-of-truth.md) | PostgreSQL is the single source of truth | Accepted |
| [0003](0003-nats-jetstream-messaging.md) | NATS JetStream for dispatch and live events | Accepted |
| [0004](0004-at-least-once-delivery.md) | At-least-once delivery, exactly-once logical transitions | Accepted |
| [0005](0005-task-lease-and-heartbeat.md) | Task leases, heartbeat renewal, fencing tokens | Accepted |
| [0006](0006-idempotency-and-side-effects.md) | Idempotency keys and declared side-effect safety | Accepted |
| [0007](0007-workflow-graph-representation.md) | Immutable DAG with CEL-guarded edges | Accepted |
| [0008](0008-checkpoint-serialization.md) | Versioned JSON checkpoints in object storage | Accepted |
| [0009](0009-agent-versioning.md) | Immutable, digest-pinned agent versions | Accepted |
| [0010](0010-tenant-isolation.md) | Shared infrastructure with server-side tenant isolation | Accepted |
| [0011](0011-worker-execution-isolation.md) | Process sandbox in Phase 1, stronger isolation on trigger | Accepted |
| [0012](0012-deployment-topology.md) | Docker Compose first, Kubernetes on documented triggers | Accepted |
| [0013](0013-execution-event-retention.md) | Bounded retention; events are audit, not source of truth | Accepted |
| [0014](0014-cancellation-semantics.md) | Sticky, best-effort, non-rollback cancellation | Accepted |
| [0015](0015-workflow-replay-semantics.md) | No event-sourced replay; re-run creates a new execution | Accepted |

## Template for new ADRs

```markdown
# ADR NNNN: Title

- **Status:** Proposed | Accepted | Superseded by ADR-XXXX
- **Date:** YYYY-MM-DD
- **Deciders:** <roles/people>
- **Related:** links to docs/ADRs

## Context
Forces, constraints, what breaks if we do nothing.

## Decision
The rule, stated so an implementer can follow it without interpretation.

## Consequences
Positive, negative, operational, and what becomes harder.

## Alternatives considered
Each with why it lost.

## Verification
How we will know the decision is implemented and still true (tests, constraints, reviews).
```
