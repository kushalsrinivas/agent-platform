# Phase 05 — NATS messaging and durable task delivery

Status: complete. This phase implements the transport between the Go control plane and the worker that executes
a committed task attempt: JetStream streams and consumers, the transactional outbox publisher, the scheduler that
creates an attempt, the worker delivery loop, fenced results, retries, dead-lettering and controlled redrive.

The Phase 05 exit criterion is met and tested against real PostgreSQL, NATS JetStream and the live API:

> A task can be published, consumed, redelivered and safely acknowledged without losing authoritative state or
> creating duplicate logical completions.

## 1. What runs

```
API ──SubmitExecution──▶ ORCHESTRATOR ──transaction──▶ PostgreSQL
                              │                        (execution, tasks, attempt, event, outbox)
                              │
                        OUTBOX PUBLISHER
                              │
                              ▼
                    NATS JetStream
                    TASK_DISPATCH  ──▶ WORKER ──AcceptTask──▶ ORCHESTRATOR
                    EVENTS         ──▶ (SSE consumers, later)      │
                    TASK_DLQ       ◀── dead letter                 │
                                                                   ▼
                                              result commit ──▶ PostgreSQL
                                                                   │
                                              broker ACK ◀─────────┘
```

Only the orchestrator writes execution state. NATS carries committed decisions, never authoritative state.
Losing, duplicating or reordering a message must not change the logical outcome.

## 2. Stream topology

The accepted architecture (ADR 0003 and [`communication.md`](communication.md#6-nats-jetstream)) defines two
streams. Phase 05 keeps that contract and adds one operational dead-letter stream.

| Stream | Subjects | Retention | Replicas (local) | Purpose |
|---|---|---|---|---|
| `TASK_DISPATCH` | `tasks.dispatch.<worker_id>` | `workqueue`, 24h, 4 GiB, 64 KiB/message | 1 (3 in a cluster) | Durable hand-off of one committed attempt to one worker. |
| `EVENTS` | `events.>` | `limits`, 72h, 2 GiB, 256 KiB/message | 1 | Execution/task lifecycle fan-out. Never read for correctness. |
| `TASK_DLQ` | `tasks.dlq.<worker_id>` | `limits`, 7 days, 1 GiB | 1 | Dead-lettered dispatches retained for inspection and redrive. |

Streams are created with `CreateOrUpdateStream`, so every service can start in any order without racing a missing
stream. `ORCHESTRATOR_STREAM_REPLICAS` controls replication; production NATS clusters use three.

The brief asks for five streams (task results, worker events, execution events, system events, dispatch). The
accepted architecture deliberately carries results and worker status over gRPC, and folds execution, task and
system lifecycle messages into the `EVENTS` subject tree. That is why there are two durable streams plus a DLQ:

| Brief concept | Where it lives |
|---|---|
| Task dispatch | `TASK_DISPATCH`, `tasks.dispatch.<worker_id>` |
| Task results | gRPC `ReportTaskResult` (the orchestrator is the only writer), emitted as `task.attempt_finished` on `EVENTS` |
| Worker events | Worker control stream (registration, heartbeat, accepted/rejected) and `EVENTS` |
| Execution events | `EVENTS`, `events.org.<org>.execution.<execution>.*` |
| System events | `EVENTS` (same envelope); operational state is in PostgreSQL and the monitoring endpoints |
| Dead letters | `TASK_DLQ`, `tasks.dlq.<worker_id>` |

This is a documented deviation from the brief, not an accidental omission. A separate results stream would create
a second writer path for execution state, which violates the architecture's single-writer invariant.

## 3. Message envelope and dispatch payload

Every NATS message uses the versioned envelope from [`docs/api/envelope.schema.json`](api/envelope.schema.json)
and [`communication.md`](communication.md#7-message-envelope). The outbox publisher sets `Nats-Msg-Id` to the
stable `message_id`.

`task.dispatch` is a pointer, not the task body:

```json
{
  "message_id": "msg_...",
  "message_type": "task.dispatch",
  "schema_version": 1,
  "tenant_id": "org_...",
  "project_id": "proj_...",
  "execution_id": "exec_...",
  "task_id": "task_...",
  "attempt_id": "att_...",
  "worker_id": "wrk_worker_01",
  "timestamp": "2026-11-23T10:00:00Z",
  "correlation_id": "exec_...",
  "payload": {
    "attempt_number": 1,
    "lease_id": "3f9c2f7a-5f36-4a4b-9d31-6b0e2f1a8c44",
    "node_id": "plan",
    "node_type": "agent",
    "agent_version_id": "agtv_...",
    "spec_digest": "sha256:...",
    "deadline": "2026-11-23T10:10:00Z",
    "lease_expires_at": "2026-11-23T10:00:30Z"
  }
}
```

`lease_id` was added to the payload in this phase. It is the fence the worker must present on acceptance and on
the result; without it a redelivered message could not prove which attempt it owns. The schema, Go and Python
models and fixture were updated together. No product deployment predates this change.

The worker reads the pinned spec, inputs and credentials from the control plane, not from the bus. The payload
stays under 64 KiB and never contains secrets.

## 4. Transactional outbox publisher

State change and notification are one transaction:

1. The orchestrator commits execution/task/attempt state, the audit event and an `execution.outbox` row.
2. The publisher claims up to `ORCHESTRATOR_OUTBOX_BATCH` due rows with `FOR UPDATE SKIP LOCKED`.
3. It publishes each message to JetStream.
4. It marks the row `published_at` or increments `attempts` and schedules a retry.

The publisher is safe to restart. An unpublished row stays due; a row claimed by a crashed publisher is released
when its transaction rolls back. A crash after publish but before commit leaves the row unpublished, so it is
published again. That duplicate is suppressed inside the NATS duplicate window by `Nats-Msg-Id` and, beyond the
window, is harmless because every consumer is fenced or idempotent.

The outbox is at-least-once, not exactly-once. No consumer may treat "I saw this message ID" as the idempotency
mechanism for a logical transition.

## 5. Scheduler, worker registration and dispatch (T2)

The scheduler runs every `ORCHESTRATOR_DISPATCH_INTERVAL_MS` (500 ms) and considers a `pending` task only when:

- its execution is `pending` or `running`;
- `next_attempt_at` is due and the attempt budget remains;
- every inbound dependency is terminal; and
- a worker in the required pool has a free reserved slot.

For each candidate it opens a transaction, locks the **execution row first**, then the task, and re-checks
eligibility. It then:

- creates a task attempt with a new UUID lease and `attempt_number`;
- CASes the task `pending → assigned`, increments `attempt_count`, stores the owner and lease;
- reserves exactly one worker slot (`reserved_slots + 1`);
- appends `task.state_changed` to the audit log and outbox; and
- inserts the `task.dispatch` outbox row.

A dispatch can never exist for an assignment that did not commit, and an assignment cannot exist without a
durable dispatch. Workers register over a bidirectional gRPC session (`Hello` → `HelloAck`), heartbeat every 5 s,
report current capacity, and renew the leases of attempts they still own. Silence for 15 s removes a worker from
assignment without stealing its leases; leases expire independently after 30 s.

Dependency activation currently uses the published graph's unconditional edges: a task activates when at least
one dependency succeeded. CEL `when` compilation was deferred in Phase 04 and remains deferred; branch-not-taken
semantics are therefore limited to the skip path described below.

## 6. Acknowledgement semantics

The five concepts are distinct:

| Concept | Owner | Durable representation |
|---|---|---|
| Message received | Worker consumer | In memory only; the broker may redeliver until acked. |
| Task accepted | Orchestrator, called by worker | Task `assigned → running`, attempt `accepted_at`, execution `pending → running`, audit event. |
| Task started | Worker | Runtime begins after `task_accepted` is sent; lease is renewed by heartbeats. |
| Task completed | Orchestrator | Attempt closed and task transition committed with the result, event and retry decision. |
| Message acknowledged | Worker consumer | JetStream `Ack` only after the orchestrator committed the result. |

A broker acknowledgement is **not** task completion. The worker order is fixed:

```
receive → validate tenant/worker/lease → send task_accepted → execute
        → ReportTaskResult (PostgreSQL commit) → JetStream Ack
```

If the worker crashes before the result commit, no acknowledgement is sent and the broker redelivers. If it
crashes after the commit but before the ack, the broker redelivers and the orchestrator answers
`ALREADY_FINISHED`; the worker acknowledges without repeating the operation. If the lease has moved on, the
orchestrator answers `STALE_LEASE` and the worker discards the message. A validation failure is a permanent
error and is dead-lettered rather than retried forever.

## 7. Duplicate delivery and fencing

The duplicate-delivery recovery is implemented and tested end to end:

1. Worker receives `task.dispatch`.
2. Worker accepts the attempt and executes.
3. `ReportTaskResult` commits the attempt result, task state and audit events.
4. The worker is killed before `Ack`.
5. JetStream redelivers the same message after `AckWait` / `Nak`.
6. The worker calls `ReportTaskResult` with the same `(attempt_id, lease_id)`.
7. The orchestrator finds the attempt closed under that lease and returns `ALREADY_FINISHED`; the task is already
   terminal, so no second completion is written.
8. The worker acknowledges the duplicate without re-executing.

The integration test `TestMessagingRedeliveryAndDuplicateSuppression` performs exactly this by committing the
result and then returning an error before the consumer can acknowledge. It asserts one attempt and one
`task.attempt_finished` event per task, two succeeded tasks, one succeeded execution, and no pending or
unacknowledged dispatch messages.

Fencing is enforced by `(task, attempt, lease, worker)` under a row lock. A duplicate `task_accepted` with the
same lease is a no-op. A result from another worker, another lease or an expired lease is `STALE_LEASE` and is
never applied.

## 8. Retry, dead-letter and redrive

Consumer policy is per worker: explicit ack, `AckWait = 60s` (`2s` in the redelivery test), `MaxDeliver = 5`,
exponential backoff `1s, 2s, 4s, 8s, 16s`, bounded `MaxAckPending = capacity`.

- A transient handler failure is `NakWithDelay` and redelivered.
- A permanent handler failure (malformed payload, wrong worker, invalid result) is dead-lettered immediately.
- Exhausting `MaxDeliver` also dead-letters.
- The consumer publishes the DLQ envelope before acknowledging the original. If the DLQ publish fails, the
  message is redelivered instead of lost.

The DLQ envelope preserves the original envelope in `payload`, the original subject, message ID, tenant, project,
execution, task and attempt IDs, the delivery count, the error text and the failure time. `TASK_DLQ` retains
messages for seven days so operators can inspect them after the incident.

Redrive is operator-controlled:

```bash
curl -H "Authorization: Bearer $ORCHESTRATOR_ADMIN_TOKEN" \
  -X POST http://orchestrator:8081/v1/messaging/dlq/redrive \
  -d '{"worker_id":"wrk_worker_01","limit":10}'
```

It republishes the original envelope to its original subject, acknowledges the DLQ message only after the
republish succeeds, and leaves the worker's normal fencing in place. A redriven message that reaches an already
completed task is acknowledged as a duplicate, not re-executed. The operator token is empty by default, which
disables both operator routes; VPS deployments do not expose the orchestrator port.

## 9. Monitoring

- `GET /v1/consumer` on the worker (port 8082) returns local consumer counters and broker pending/redelivery
  numbers.
- `GET /v1/messaging/status` on the orchestrator (port 8081, operator token) returns unpublished outbox rows,
  oldest due age, retrying rows, per-stream messages/bytes/consumers, dispatch consumer lag/redelivery and the
  worker fleet with reserved slots.
- `GET /healthz` / `/readyz` on the worker require NATS and a registered control session.
- Recommended alerts: outbox oldest due age > 30 s, dispatch consumer pending > 0 with zero active workers, DLQ
  growth, worker heartbeats stale, and `reserved_slots == max_concurrency` for a sustained period.

## 10. Configuration

| Setting | Default | Meaning |
|---|---|---|
| `ORCHESTRATOR_STREAM_REPLICAS` | `1` | JetStream replicas; use 3 with a NATS cluster. |
| `ORCHESTRATOR_DISPATCH_INTERVAL_MS` | `500` | Scheduler scan interval. |
| `ORCHESTRATOR_REAPER_INTERVAL_MS` | `2000` | Lease-expiry scan interval. |
| `ORCHESTRATOR_OUTBOX_INTERVAL_MS` | `250` | Publisher poll interval. |
| `ORCHESTRATOR_OUTBOX_BATCH` | `50` | Maximum rows per publish pass (1–100). |
| `ORCHESTRATOR_OUTBOX_RETRY_SECONDS` | `5` | Delay before retrying a failed publish. |
| `ORCHESTRATOR_ADMIN_TOKEN` | empty | Enables operator status/redrive when non-empty. |
| `WORKER_ID` | `wrk_<hostname>` | Stable worker identity; must match `^wrk_[A-Za-z0-9_-]{8,64}$`. |
| `WORKER_POOL` | `default` | Placement pool matched by node `worker_pool`. |
| `WORKER_MAX_CONCURRENCY` | `4` | Consumer `MaxAckPending` and reserved-slot cap. |
| `WORKER_ORCHESTRATOR_TLS` | `false` | Use TLS for the worker control channel; local Compose is private plaintext. |
| `WORKER_ORCHESTRATOR_GRPC_ADDR` | `orchestrator:9090` (Compose) | Orchestrator control address. |

## 11. Verification

```bash
make test                                        # Go race tests + Python contracts/SDK
make lint                                        # gofmt, vet, golangci-lint, Ruff, mypy, Biome
make integration-test                            # real API + PostgreSQL + NATS + worker
```

Integration suites:

- `infrastructure_test.go` — JetStream availability and schema/role separation.
- `schema_test.go` — RLS, immutability, outbox claim/mark/skip-locked.
- `api_flow_test.go` — Phase 04 API flow, now tolerant of immediate scheduling.
- `messaging_flow_test.go` — Phase 05:
  - `TestMessagingRedeliveryAndDuplicateSuppression` — commit, crash before ack, redelivery, duplicate
    suppression, one attempt per task, one completion event per task.
  - `TestMessagingDeadLetterAndRedrive` — poison dispatch, DLQ envelope contents, operator redrive, successful
    completion.

The manual smoke check is: dev token → org → project → agent version/publish → workflow version/publish →
execution; the execution reaches `succeeded` in about two scheduler ticks with two succeeded tasks and one
succeeded attempt per task.

## 12. Deliberate deviations and follow-ups

| Item | Status | Reason / trigger |
|---|---|---|
| Five separate streams | Collapsed to `TASK_DISPATCH`, `EVENTS`, `TASK_DLQ` | ADR 0003 and the single-writer invariant; results travel over gRPC. |
| Task results on NATS | Not used | The orchestrator must be the only writer of execution state; the fenced gRPC result is the durable commit. |
| Python runtime execution | Deferred | The Go worker consumes, holds the lease and reports the result. The deterministic echo executor proves the transport; the sandboxed Python runtime replaces `delivery.Echo` in the runtime phase. |
| CEL `when` compilation | Deferred from Phase 04 | Activation uses unconditional edges; a task with no succeeded inbound edge is skipped as `dependency_failed` / `upstream_not_taken`. |
| Cancellation propagation | Deferred | The control stream exposes cancel/drain messages, but the API cancel path and in-flight kill are the next lifecycle slice. Lease expiry is the bounded backstop. |
| Retry `retry_on` policy | Partial | Retries use failure classification and `max_attempts`; the per-node allow-list is not yet read from the pinned graph. |
| Non-idempotent side effects | Not enforced | A non-idempotent node must set `max_attempts = 1` or an idempotency key; validation exists, runtime enforcement follows with the real executor. |
| mTLS worker identity | Deferred | Local control-plane gRPC is plaintext inside the Compose network; production TLS is an infrastructure task. The worker identity header is not trusted outside that network. |
| SSE / `EVENTS` consumer | Deferred | The event stream is published and durable; the API event fan-out is a later slice. |
| Redrive UI / automatic DLQ policy | Deferred | Redrive is an operator API; there is no dashboard. |
