# ADR 0003: NATS JetStream for dispatch and live events

- **Status:** Accepted
- **Date:** 2026-11-22
- **Deciders:** Architecture
- **Related:** [communication.md](../communication.md), [adr/0002](0002-postgres-source-of-truth.md)

## Context

Workers need durable, pull-based task delivery with per-worker addressing and explicit acknowledgement. The
dashboard needs low-latency lifecycle events. We evaluated Kafka, RabbitMQ, Redis Streams, SQS and NATS JetStream.
Phase 1 is a single-region deployment with a small platform team; operability and a Go-native client matter.

The key insight is that **the broker is not the source of truth**. PostgreSQL already holds assignments, leases and
events. The broker only needs to deliver notifications reliably enough to be useful, while the reaper guarantees
progress when it does not.

## Decision

- **NATS JetStream** is the initial messaging system.
- Stream `TASK_DISPATCH` is a work-queue stream with per-worker subjects `tasks.dispatch.<worker_id>`. Each worker
  has a durable pull consumer filtered to its own subject, explicit ack, bounded `max_deliver`, and a dead-letter
  subject `tasks.dlq.<worker_id>`.
- Stream `EVENTS` is a limits stream with subjects `events.org.<org_id>....`, retained 72h for live fan-out.
- Publishing uses the **transactional outbox** in PostgreSQL. Assignment/result transactions write outbox rows;
  the publisher claims them with `FOR UPDATE SKIP LOCKED`, publishes, and marks them.
- At-least-once delivery is accepted. Duplicate suppression is the receiver's job via leases, fences, unique keys
  and event sequence numbers.
- NATS is not used for commands that require a response. Worker control (accept, cancel, heartbeat, credentials)
  uses gRPC; the bus carries dispatch and events only.
- Authentication/authorization uses per-worker NATS credentials in production, restricted to its dispatch subject
  and DLQ. The API's account can read only `EVENTS`.

## Consequences

**Positive**

- One small binary with streams, pull consumers and subject filtering; low operational overhead locally.
- Per-worker subjects give a natural, observable hand-off without a scheduler-managed queue per worker.
- Outbox decouples broker availability from state correctness.
- Event subjects carry tenant IDs, enabling simple filtered fan-out.

**Negative**

- No exactly-once delivery and no global ordering; consumers must be idempotent.
- Per-worker durable consumers add broker objects proportional to the fleet; cleanup is required.
- JetStream persistence/tuning (file store, replicas, max bytes, duplicate window) is its own operational skill.
- Duplicate suppression window (2m) is shorter than some outage windows; correctness cannot rely on it.

**Operational**

- Production uses R3 stream replication and a 3-node NATS cluster; Phase 1 may use a single node with file storage.
- Monitor consumer lag, pending messages, redelivery rate, DLQ growth and stream byte usage.
- Losing the broker delays work and live UI; it never corrupts state. This is tested by deleting stream state and
  observing reaper recovery.

## Alternatives considered

- **Kafka.** Excellent durability/ordering, but heavier operationally and its partition model is a poor fit for
  per-worker assignment; ordering is not needed for correctness. Rejected for Phase 1, could return at scale.
- **RabbitMQ.** Mature queues, but JetStream's pull consumer model and single binary better match per-worker
  dispatch, and we avoid managing two clustering systems.
- **Redis Streams.** Simple, but durability and consumer-ack semantics under failover are weaker; also risks making
  Redis look authoritative.
- **SQS (or cloud queue).** Durable and managed, but adds cloud coupling, lacks the lightweight local story, and
  still requires the same fencing. Kept as a future adapter behind the publisher interface.

## Verification

- An integration test stops NATS during assignment, proves the outbox retains the dispatch, and proves reaper
  recovery without a logical failure.
- Duplicate dispatch and duplicate event tests confirm no duplicate state transition and no duplicate SSE `seq`
  application.
- A worker can consume only its own dispatch subject under production credentials.
- Outbox lag and DLQ growth are alertable metrics.
