# Phase 06 — Worker manager and distributed scheduler

Status: complete. This phase turns the single scheduler loop from Phase 05 into a distributed worker manager:
workers register capabilities and resources, heartbeat capacity and usage, receive work through a
capability- and fairness-aware scheduler, hold database-backed leases, renew them, and drain gracefully.

The scheduler is independent of the runtime. It reasons about task requirements — node type, pinned runtime ABI
and image digest, worker pool, priority, tenant and provider limits — never about how an agent reasons or which
model it calls.

The exit criterion is met and tested against multiple worker processes, real PostgreSQL and JetStream:

> Multiple worker instances can register, receive different tasks, renew leases and become unavailable without
> corrupting task state.

## 1. Worker registry

Registration is the first control-stream message (`Hello`). The orchestrator persists:

| Field | Meaning |
|---|---|
| `id`, `boot_id`, `session_id` | Stable worker identity, one process boot, one current control session. |
| `pool` | Placement pool matched by node `worker_pool`. |
| `runtime`, `runtime_version` | Runtime family and version (`python`, `0.1.0`). Distinct from the Go host `binary_version`. |
| `capabilities[]` | Advertised work types (`agent_execution`, `tool_execution`, future `langgraph`, `mcp`). |
| `cpu_cores`, `memory_mb` | Total resources available to the worker host. |
| `max_concurrency`, `reported_available_slots` | Capacity and the worker's latest advisory free slots. |
| `runtime_abis[]`, `runtime_digests[]` | Runtime ABIs and image digests the worker can execute. An empty digest list is a development wildcard. |
| `labels`, `protocol_version` | Placement labels and protocol version. |

The registration upsert replaces a previous boot for the same worker ID. When `boot_id` changes, the previous
boot's open attempts are closed immediately as `lease_expired` and returned to the scheduler; they are not left for
the lease TTL. A late result from the old process is then fenced by the closed attempt. The worker ID is never
reused for a different task.

Worker statuses are explicit:

| Status | Meaning |
|---|---|
| `starting` | Registered but not yet ready for assignment. Reserved for a two-phase runtime boot. |
| `available` | Heartbeat current and at least one slot free. |
| `busy` | Heartbeat current and no slot free. |
| `draining` | Shutting down; never assigned new work. |
| `unhealthy` | No heartbeat for 15 s; excluded from assignment, leases still expire individually. |
| `terminated` | Control session ended or silent for 60 s; only history remains. |

`available` and `busy` are derived from reserved slots and reported capacity on registration, heartbeat, dispatch
and release. `draining`, `unhealthy` and `terminated` are authoritative and are never overwritten by a capacity
calculation.

## 2. Heartbeat

Every 5 s the worker sends: last-sent time, active `(task_id, attempt_id, lease_id)` list, capacity
(`max_concurrency`, `available_slots`), CPU percentage, memory MB, runtime version and its own status.

The orchestrator updates `last_heartbeat_at`, capacity, usage and status, then renews each listed lease. Renewal
has one hard guard:

```sql
AND lease_expires_at > now()
```

A lease that already expired is never revived by a late heartbeat. Renewal also stops for `failing` or
`cancel_requested` executions. This is the boundary that makes a partition or a paused process lose ownership.

CPU and memory are process-level samples taken by the Go worker host (`/proc`-style process CPU time and
`runtime.MemStats`). They are advisory placement signals and operator telemetry. Task-level CPU/memory requests
are deferred until the agent spec carries resource requests.

## 3. Scheduling pipeline

A candidate is a `pending` task whose execution is `pending` or `running`, whose `next_attempt_at` is due, whose
attempt budget remains, and whose inbound dependencies are all terminal. For each candidate the dispatcher locks
the execution row first, then the task, and re-checks under the lock. Before assignment it verifies:

1. **Task eligibility** — CAS from `pending`, dependency activation.
2. **Execution status** — only `pending` or `running`.
3. **Worker capability** — `agent_execution` for agent nodes, `tool_execution` for tool nodes; runtime ABI and
   image digest must match when the worker advertises them.
4. **Tenant concurrency** — optional `ORCHESTRATOR_MAX_ACTIVE_TASKS_PER_TENANT` cap on `assigned`/`running` tasks
   per organisation.
5. **Provider concurrency** — optional `ORCHESTRATOR_PROVIDER_CONCURRENCY` cap on active tasks for the pinned
   model provider, globally across tenants.
6. **Tool concurrency** — optional `ORCHESTRATOR_TOOL_CONCURRENCY` cap on active tasks for a pinned tool version.
7. **Lease acquisition** — new UUID lease token, monotonic attempt number, expiry from the database clock.
8. **Worker assignment** — reserve exactly one slot, move the worker to `busy` when full, write the attempt and
   the `task_dispatch` outbox row in the same transaction.

### Queue order and starvation

The candidate query ranks tasks per tenant and then interleaves tenants:

```sql
row_number() OVER (PARTITION BY org_id ORDER BY priority DESC, created_at, id) AS tenant_rank
effective_priority = priority + LEAST(age_minutes, 200)
ORDER BY tenant_rank, effective_priority DESC, created_at, id
```

This gives, in order:

- **FIFO** when all priorities are equal: age dominates and ties break by creation time.
- **Priority** within a tenant: higher `priority` (−100..100, default 0) first.
- **Capacity-aware placement**: only workers with `reserved_slots < LEAST(max_concurrency,
  reported_available_slots)` are selected; lower reservation and lower CPU usage win ties.
- **Tenant-aware fairness**: the primary `tenant_rank` key interleaves one task per tenant before any tenant's
  second task, so a large tenant cannot fill a dispatch pass.
- **Anti-starvation**: aging adds one effective priority point per minute waited, capped at 200. A normal task
  outranks a new maximum-priority task after 100 minutes, and a configured per-tenant active cap is a hard bound.

## 4. Task assignment and leases

Every assignment writes, in one transaction:

- `task_attempts`: attempt ID, worker, UUID lease token, attempt number, dispatch time;
- `execution_tasks`: `assigned`, owner, lease, expiry, `latest_attempt_id`, incremented attempt count;
- worker slot reservation and derived status;
- `task.state_changed` audit event and outbox;
- `task.dispatch` outbox row for the worker-specific NATS subject.

The attempt number is the monotonic generation; the lease token is the ownership secret. Results must present
`(task_id, attempt_id, lease_id, attempt_number)`. A result is applied only when all of those match the open
attempt and the lease is unexpired. A duplicate result for a successfully closed attempt returns
`ALREADY_FINISHED`; a result for an attempt closed as `lease_expired` or `incomplete` returns `STALE_LEASE`. Neither
writes state. A stale worker therefore cannot commit after the orchestrator reclaimed ownership, even if it
reconnects and its worker row is still active.

### Reserved-slot accounting

Dispatch increments `reserved_slots` and every terminal/retry/reap path decrements it in the same transaction as
the task state change. The orchestrator reconciles the counters from actual `assigned`/`running` attempts once at
startup, before the dispatcher accepts work. It is deliberately not a periodic job: a count computed concurrently
with dispatch can overwrite a fresh reservation. Execution deletion is not a production operation; test cleanup
resets its own workers.

## 5. Graceful shutdown

The worker handles `SIGTERM`/`SIGINT` (and a control-plane `DrainWorker` message) in this order:

1. `BeginDrain`: report `draining` in an immediate heartbeat; reject any prefetched dispatch with
   `SHUTTING_DOWN`, which returns the task to `pending`.
2. Stop the consumer: finish in-flight handlers within the broker drain window (10 s).
3. Wait up to `WORKER_SHUTDOWN_GRACE_SECONDS` (default 25 s) for active attempts to reach zero. Heartbeats keep
   renewing their leases while they run and report results with the reporting context, not the cancelled
   execution context.
4. `Shutdown`: send `Goodbye`; the orchestrator records the worker `terminated` for the current session.
5. Exit. Any attempt still open is left to the lease reaper, which closes it `lease_expired` and retries per
   policy. A draining worker reconnecting before the goodbye registers with zero available slots, so it cannot be
   assigned work in the reconnect window.

Checkpoint persistence before shutdown is a runtime responsibility; the Phase 06 echo executor has no checkpoint.
The runtime phase must checkpoint a cancelled/expiring attempt before its process is killed.

## 6. Resource tracking

`execution.workers` stores total CPU cores, total memory, latest CPU percent and latest memory used. The admin
status endpoint returns them with capabilities, runtime and slot counts. Scheduling uses CPU usage as a
tie-breaker among equally free workers. Provider/tool/tenant limiter counters are derived from active task rows,
not from in-memory counters, so they survive orchestrator restarts and are consistent across replicas.

## 7. Configuration

| Setting | Default | Meaning |
|---|---|---|
| `ORCHESTRATOR_LEASE_SECONDS` | `30` | Lease TTL; heartbeats renew unexpired leases. |
| `ORCHESTRATOR_HEARTBEAT_TIMEOUT_SECONDS` | `15` | Silence before a worker is marked `unhealthy`. |
| `ORCHESTRATOR_WORKER_TERMINATE_SECONDS` | `60` | Silence before a worker is marked `terminated`. |
| `ORCHESTRATOR_MAX_ACTIVE_TASKS_PER_TENANT` | `0` | Per-organisation active-task cap; 0 disables. |
| `ORCHESTRATOR_PROVIDER_CONCURRENCY` | empty | `provider=limit,...`; global active-task cap per model provider. |
| `ORCHESTRATOR_TOOL_CONCURRENCY` | empty | `tool=limit,...`; global active-task cap per tool version. |
| `WORKER_RUNTIME` | `python` | Runtime family advertised at registration. |
| `WORKER_RUNTIME_VERSION` | `0.1.0` | Runtime version advertised at registration and heartbeat. |
| `WORKER_CAPABILITIES` | `agent_execution,tool_execution` | Comma-separated capabilities. |
| `WORKER_CPU_CORES` | `0` | Total CPU cores; 0 uses host `NumCPU`. |
| `WORKER_MEMORY_MB` | `2048` | Total memory advertised. |
| `WORKER_HEARTBEAT_MS` | `5000` | Control heartbeat interval. |
| `WORKER_SHUTDOWN_GRACE_SECONDS` | `25` | Maximum wait for active attempts during shutdown. |

Execution priority is accepted by `POST .../executions` as `priority` (−100..100, default 0) and copied to every
task at materialisation. The idempotency digest includes priority.

## 8. Verification

```bash
make test                 # unit/race tests, Python contracts
make lint
make integration-test     # Compose stack with API, orchestrator and worker
```

Phase 06 integration scenarios (all pass against Docker):

- `TestWorkerManagerDistributesTasks` — two workers with `max_concurrency = 1` receive two independent tasks;
  both complete, one task per worker.
- `TestWorkerManagerPriorityOrder` — a priority-100 execution is assigned before a normal-priority execution of
  the same tenant when both are pending.
- `TestWorkerManagerTenantFairness` — two tenants with two tasks each and two workers get one task each on the
  first dispatch pass.
- `TestWorkerManagerLeaseRenewal` — a 3-second task's lease expiry advances while it executes; heartbeats renew it.
- `TestWorkerManagerStaleWorkerCannotCommit` — a worker is stopped, its lease is reclaimed, another worker
  completes the retry, and the old worker's late result returns `STALE_LEASE` with exactly one succeeded attempt.
- `TestWorkerManagerDrainStopsAssignments` — a draining worker is marked `draining` and receives no new work.
- `TestWorkerManagerBootChangeReclaimsAttempts` — a replacement process with the same worker ID and a new boot
  immediately reclaims the previous boot's open attempt and it is reassigned.

The Phase 05 redelivery, duplicate suppression and DLQ/redrive tests continue to pass.

## 9. Deliberate deviations and follow-ups

| Item | Status | Reason / trigger |
|---|---|---|
| Python worker processes | Implemented in Phase 07: the Go host spawns one Python runtime process per attempt over a Unix socket. | The scheduler and lease protocol stay runtime-independent; the worker host remains the only NATS/DB writer. |
| Operator-initiated drain | The control stream handles `DrainWorker` and the worker reacts, but no public endpoint/gRPC command is wired yet. | SIGTERM shutdown is complete; expose `DrainWorker` when `platformctl` or an admin API lands. |
| Checkpoint before shutdown | Deferred to the runtime phase. | No runtime execution or checkpoint bytes exist yet. |
| Task-level CPU/memory requests | Tracked but not enforced; worker CPU/memory are placement metadata. | The agent spec has no resource-request fields yet. Add them with the sandbox. |
| Provider/tool/tenant limit tests | Parser and enforcement implemented; not exercised in integration because limits are process configuration. | Add per-test orchestrator instances or an admin limit override when there is a dedicated scheduler test harness. |
| `starting` status | Defined and persisted, not emitted by the current one-phase boot. | The runtime sandbox needs a two-phase ready signal. |
| Worker IDs | Contract IDs use the `wrk_` prefix, not the illustrative `worker_123`. | The Phase 05 envelope contract already fixed the pattern; changing it would break the message schema. |
| mTLS worker identity | Deferred; local control-plane gRPC is private plaintext. | Infrastructure task; the lease fence does not depend on transport identity. |
