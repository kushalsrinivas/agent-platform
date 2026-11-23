# ADR 0013: Execution event retention

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [data-ownership.md](../data-ownership.md), [communication.md](../communication.md)

## Context

Execution events, attempt results, checkpoints and logs grow without bound if kept forever. They contain tenant
data and prompt/tool content, so retention is also a privacy decision. But events are the audit trail for who
changed what, and customers need to debug runs after the fact. We need explicit lifetimes, a deletion mechanism and
a rule for legal holds.

The event log is **not** the source of truth (ADR 0015). Losing old events must not make current state
unrecoverable, only less explainable.

## Decision

### Lifetimes

| Data | Default | Notes |
|---|---|---|
| Execution, task, attempt metadata | 13 months after terminal | Batched deletes indexed by terminal time. |
| `execution_events` (audit) | 13 months after terminal | Same retention horizon as the run; legal hold overrides. |
| Inline attempt result payload | While execution is non-terminal, then 30 days after attempt finish | Row kept; `result_inline` set to NULL. |
| Checkpoints | While execution is non-terminal, then 30 days or latest 2 per task | Object lifecycle + metadata tombstone. |
| Large output artifacts | While execution is non-terminal, then 30 days | Object lifecycle. |
| Task logs | While execution is non-terminal, then 14 days | Object lifecycle; logs are not audit. |
| Outbox published rows | 7 days | Operational only. |
| `workers` rows | 7 days after `offline` | Attempts retain the worker ID. |
| Idempotency keys | 24h after linked execution terminal, max 7 days | Prevents replay while allowing retries. |
| Published agent/workflow versions | Indefinite in Phase 1 | Required for audit; soft-delete parents only. |
| NATS `TASK_DISPATCH` | 24h | `workqueue` stream limit. |
| NATS `EVENTS` | 72h | Live fan-out only; catch-up is from PostgreSQL. |
| Traces/metrics/logs (OTel backend) | Per backend policy (typ. 7–30 days) | Not authoritative; no correctness dependency. |

### Mechanism

- Retention runs as bounded, indexed deletes keyed by terminal time (`ended_at`/`finished_at`), oldest first, with
  pauses to protect write latency. Phase 1 does **not** partition these tables; partitioning is an optimisation to
  adopt when table/bucket volume justifies it. If adopted, keys and unique constraints must include the partition
  key, and per-execution event `seq` uniqueness must still be guaranteed by the allocator under the execution row
  lock. That requires a new ADR.
- **No payload, blob, checkpoint, artifact or log referenced by a non-terminal execution is deleted**, regardless of
  age. The horizons below apply from terminal time, or to data no longer needed by an active run.
- Payload redaction runs before deletion: inline results and checkpoint bytes are removed at 30 days after the
  attempt/terminal while the metadata remains for audit.
- Object lifecycle rules delete checkpoints/artifacts/logs; the metadata row is tombstoned or deleted in the same
  window. A blob with no committed DB reference is GC'd after 24h regardless.
- Deletion is blocked for executions that are not terminal. Cancellation/timeout finalisation runs first.
- Deletion never crosses a legal hold. Holds are recorded per execution/tenant and override all defaults; lifting a
  hold resumes normal retention from that point.
- A tenant offboarding export can extend retention temporarily; it must be explicit and audited.

### Why these numbers

- 13 months covers annual audits and "same time last year" debugging without indefinite prompt retention.
- 30 days for payloads/checkpoints balances resume/debug value against sensitive content.
- 14 days for logs reflects their size and low audit value; traces/events carry the durable story.
- 72h events in NATS is enough for a long weekend reconnect; the database is the catch-up source.

## Consequences

**Positive**

- Predictable storage growth and a defensible privacy story.
- Audit trail covers a full annual cycle.
- Old runs remain explainable through state + metadata even after large payloads are gone.
- Legal hold is explicit rather than "we never delete".

**Negative**

- Debugging a run older than 30 days loses output/checkpoint detail; only metadata/events remain.
- Retention is delete-heavy; without partitioning it costs autovacuum I/O and needs throttling. This is accepted
  until volume proves otherwise.
- Customers who expect indefinite event history need a paid/export path (not Phase 1).
- Deleting checkpoints can make an old failed task unresumable; acceptable because retries of old executions are
  not a product feature.

## Alternatives considered

- **Retain everything forever.** Simple, but unbounded cost and privacy risk; not acceptable for prompt data.
- **30-day retention for everything.** Cheaper, but weak audit and poor customer debugging.
- **Event sourcing with infinite event history as the product archive.** Pushes storage and privacy problems
  without needing the events for state (ADR 0015).
- **Delete on terminal immediately.** Fastest privacy win, but destroys audit and supportability.
- **Keep only NATS events (72h).** Loses audit and breaks after a broker outage.

## Verification

- Retention job tests use a clock injection and assert payloads/rows disappear only after the horizon.
- A non-terminal execution is never deleted, even if malformed timestamps suggest it is old.
- Legal hold tests prove deletion is blocked and resuming works.
- A restore drill confirms old terminal executions are readable within the horizon.
- Storage/cost dashboards show table and bucket growth by tenant against the policy.
