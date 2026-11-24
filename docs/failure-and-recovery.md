# Failure and Recovery Design

Normative. The question this document answers is not "how do we avoid every failure" but **"which failures can
change the logical outcome, and which cannot"**.

## 1. Recovery principles

1. **Commit before claim.** Nothing is accepted, dispatched or completed until PostgreSQL says so. A crash before
   commit means it never happened; a crash after commit means the work is recoverable from rows.
2. **Re-derive, don't remember.** On restart, services rebuild their view from PostgreSQL and the outbox. In-memory
   queues, capacity views and subscriptions are disposable.
3. **Fencing over coordination.** A stale worker is defeated by a lease compare, not by hoping it noticed. Two
   components may *think* they own a task; only one lease can commit results.
4. **At-least-once delivery, exactly-once effect on state.** Delivery duplication is absorbed by guarded
   transitions. External side effects are not magically exactly-once; their safety is declared and enforced by
   policy (ADR 0006).
5. **Fail visible, fail closed for tenancy.** Missing tenant context returns no rows. Unknown message versions are
   ignored and counted. A worker without a valid session gets no work.
6. **Quiescence before terminal.** `failed`/`cancelled` mean no task can still be executing under a valid lease.

## 2. Failure matrix

| Failure | Detection | State impact | Recovery | Residual risk |
|---|---|---|---|---|
| API instance dies | health check | None; it owns no execution state | Request retried; load balancer reroutes; SSE reconnects with `Last-Event-ID` | A mutation that committed but whose response was lost looks failed to the caller; idempotency key resolves it |
| API loses database read access | readiness fails | Queries/SSE fail; command forwarding may still work | Retry; read replica not used in Phase 1 | Dashboard degradation only |
| Orchestrator instance dies | health check, missed gRPC streams | No committed state lost | Another replica serves; workers reconnect; the recovery manager resumes; outbox publisher resumes | In-flight gRPC calls fail and are retried by clients idempotently |
| Orchestrator dies after commit, before reply | client timeout | State committed; caller unsure | Client retries submission with same idempotency key; gets existing execution | None logical |
| Orchestrator dies after task CAS, before outbox publish | committed outbox row | Task assigned, dispatch not yet sent | Restarted publisher sends it; lease may expire first (§3.2) | Retry budget accounting for never-sent dispatch (§3.2) |
| Orchestrator dies after result commit, before reply | worker RPC timeout | Task/execution transition committed | Worker retries; gets `ALREADY_FINISHED`/`STALE_LEASE` | None |
| Publisher dies after NATS publish, before mark | outbox row still unpublished | Message exists twice after restart | Duplicate publish; worker fence makes it harmless; broker dedupe may collapse it | None logical |
| PostgreSQL primary fails | connection errors, health | No writes; reads may fail | Managed failover or restore; see §6 | With async replication, a small window of committed data can be lost; prod requires synchronous replication |
| PostgreSQL unavailable briefly | timeouts | Scheduling, results and cancels stall | Outage window: leases expire without anyone to reap; on recovery the reaper processes them | Burst of reassignments; bounded by batch size/jitter |
| NATS unavailable | publish errors, consumer lag | State commits continue; dispatches/events queue in outbox; SSE live pauses | Publisher retries; API serves DB catch-up; scheduler pauses assignments (§3.5) | Delayed starts; no logical corruption |
| NATS loses stream data | stream/consumer errors, gaps | Dispatches may be missing; events lost | Leases reaped; outbox republished if unpublished; SSE catch-up from PostgreSQL | Start latency; live UI gap until refresh |
| Worker process dies | heartbeat stops | Leases expire; attempts marked `lease_expired` | Reaper retries or fails per policy; another worker continues | Side effect may have happened; `effect_uncertain` set |
| Worker partitioned from orchestrator but sandbox alive | heartbeat stops; gRPC stream breaks | Orchestrator may reassign after 30s; old sandbox can still run | Worker kills active attempts after losing the control stream beyond lease TTL; late result rejected by fence | Concurrent execution window; duplicate external effects possible for non-idempotent nodes |
| Worker partitioned from NATS only | consumer lag, heartbeat OK | No new dispatches; in-flight continues | Reconnect; backlog delivered; leases remain valid | Delay only |
| Worker reconnects after being declared dead | new `Hello` | Old session/leases stale | Orchestrator invalidates old session; worker kills orphaned sandboxes; stale results rejected | Orphan process cleanup on worker startup is mandatory |
| Two orchestrator replicas reap | advisory lock contention | Only one holds the reaper lock | Loser retries next tick | None |
| Worker sends duplicate result | duplicate RPC | First valid commit wins | Second gets `ALREADY_FINISHED`/`STALE_LEASE` | None |
| Duplicate dispatch | duplicate pull delivery | One attempt; worker checks fence | Discard/ack if not open | Wasted work only |
| Python runtime OOM/crash | exit code, supervisor | Attempt fails `RUNTIME_CRASH` | Retry policy; checkpoint may allow resume | Lost since last checkpoint; external effects possible |
| Python hangs | task deadline | Worker SIGTERM/SIGKILL; reaper also expires lease | `TASK_TIMEOUT`; retry policy | Same as above |
| Control node crashes mid-decision | pending task, committed dispatcher transaction | Nothing partially committed | Re-evaluated on the next pass | None |
| Child execution fails/cancels | parent task polls child state | Parent remains pending | Parent fails `SUBWORKFLOW_FAILED`; loop follows its policy | Child side effects may have occurred |
| Approval/delay waits forever | no operator/signal | Task stays pending | Operator signal, execution cancellation or deadline | Delayed work, never duplicated |
| Artifact volume unavailable | filesystem errors | Large checkpoints cannot be offloaded; small states stay inline | Retry upload; task retry from an earlier inline checkpoint; `require_checkpoint` fails the attempt | Resume granularity; large-state nodes fail until the volume returns |
| Checkpoint corrupt/mismatched | hash mismatch, schema/ABI mismatch | Attempt fails `CHECKPOINT_INVALID`/`CHECKPOINT_INCOMPATIBLE` | Restart node only if policy allows; otherwise fail | Progress loss |
| Database migration fails mid-deploy | migration job | Deploy blocks; old version continues if compatible | Roll forward with a corrective migration; expand/contract avoids destructive steps | Operational only |
| Clock skew | NTP alerts | Leases/deadlines unaffected (DB clock); log/trace timestamps may skew | NTP; timestamps diagnostic except DB | Misleading debugging |
| Tenant filter bug | tests, audits, alerts | Potential cross-tenant read/write | Treat as security incident; RLS limits blast radius | Depends on path |

## 3. Detailed scenarios

### 3.1 Orchestrator crash at each pipeline point

| Crash point | Durable state | Recovery |
|---|---|---|
| Before submission transaction commits | Nothing | Client retries; no execution exists. API must not have returned 202. |
| After submission commit, before API response | Execution + tasks + outbox | Client retries with the same `request_id`; orchestrator returns the existing execution. Publisher sends `execution.created` eventually. |
| After assignment commit, before publish | `assigned` task + attempt + outbox row | Publisher resumes. If the lease expires before delivery, §3.2 applies. |
| After publish, before NATS ack | Message possibly delivered | Duplicate is harmless; worker fence/attempt check. |
| While handling a result, before commit | Nothing changed | Worker retries. Reaper may also expire the lease; whichever transaction commits first wins. |
| After result commit, before gRPC reply | Committed | Worker retries, receives `ALREADY_FINISHED`/`STALE_LEASE`, stops. |
| While finalising cancellation, before commit | Still `cancel_requested` | Cancel command/heartbeats/reaper continue; finalisation is idempotent. |
| After terminal commit, before SSE event | Terminal in DB, outbox committed | Publisher sends later; client refetch/replay sees terminal. |

### 3.2 Dispatch never sent (NATS down or publisher crash)

A task is assigned and a lease starts, but the dispatch message has not been published (`outbox.published_at IS
NULL`). This is an internal failure window, not a worker failure.

- The scheduler pauses new assignments when the publisher is unhealthy or the oldest unpublishable outbox row is
  older than 10s. Assignments already committed are allowed to drain.
- The reaper detects a lease expiry for an attempt whose dispatch was never published. It closes the attempt as
  `incomplete`, returns the task to `pending`, and **does not charge the retry budget** (the attempt never reached
  a worker). This is the only case where `attempt_count` is decremented; it is logged and alerted.
- Therefore a NATS outage cannot burn retries or turn a healthy execution into a failure. It only delays starts.

### 3.3 Worker crash and zombie fencing

1. Heartbeats stop; after 15s the worker is unhealthy and receives no new work. Leases expire at 30s.
2. Reaper closes the open attempt `lease_expired` with `outcome_unknown=true` and, if `started_at` is set,
   `effect_uncertain=true`.
3. The frozen retry policy decides: `pending` (new attempt, resuming the newest readable checkpoint) or `failed`
   and `dead_lettered_at`. An uncertain non-idempotent side effect is not retried.
4. If the worker is merely partitioned, it may continue and later report. The fence compares the **current** lease,
   which no longer matches. The report is rejected; the worker kills the sandbox. Fencing protects the database, not
   the outside world: the external effect may already have occurred. This is exactly why non-idempotent nodes must
   not be retried on `worker_lost` unless they carry an idempotency key.
5. On reconnect or startup, a worker must enumerate and kill any sandbox process it still owns, delete credential
   material and report nothing as success unless it has a live lease. Orphan cleanup is not optional.

### 3.4 Duplicate and out-of-order deliveries

- **Two dispatches of the same attempt** (broker redelivery, publisher retry): the worker checks with the
  orchestrator; only one lease matches. The second is discarded and acked. If both races reach `task_accepted`, the
  CAS on `(state='assigned', owner, lease)` admits one transition; the duplicate sees `running` under the **same**
  lease and is acknowledged idempotently (it is not a stale lease).
- **A new attempt dispatch arrives before the old one's result**: the old result is fenced out. The old worker may
  still be running; the orchestrator sent a cancel command when it expired the lease, but it cannot guarantee the
  process died. The effect-uncertainty policy covers the gap.
- **NATS redelivery after ack loss**: the worker may see the same dispatch again. It must reject any attempt that is
  not open under its current lease. On **process restart**, the worker presents a new `boot_id` at registration; the
  orchestrator invalidates every open attempt owned by the previous boot of that worker, closes it as
  `lease_expired` with `outcome_unknown=true`, and lets the retry policy decide. This happens before redelivery can
  be accepted, so a restarted worker cannot accidentally execute the same attempt twice. A reconnect with the
  **same** `boot_id` is a network/session reconnect: leases are resumed only for attempts the worker still owns and
  reports in its heartbeat, never for attempts it has lost.
- **Duplicate result**: fenced; first valid wins.
- **Duplicate API submission**: `(org_id, request_id)` unique.

### 3.5 NATS outage and recovery

- Writes to PostgreSQL, task results, cancellation propagation over gRPC and SSE catch-up from the database continue
  to work. Only new dispatch and live event fan-out are lost.
- Scheduler pauses new assignments when the publisher has not drained the outbox for 10s, avoiding leases that
  cannot be delivered.
- On recovery, the publisher drains the outbox in creation order per worker, and workers drain their consumers.
  States are re-checked at every hop; stale dispatches are dropped.
- If NATS loses all stream state, unpublished outbox rows still have the truth. Published-but-lost dispatches
  surface as lease expiry; the reaper retries. In-flight workers whose results are accepted are unaffected.

### 3.6 PostgreSQL outage and failover

- All services treat the database as unavailable rather than guessing. API returns `503` with `Retry-After`
  (readiness fails); orchestrator stops scheduling and result commits; workers keep executing until their lease
  cannot be renewed, then stop new work and kill sandboxes after the TTL.
- On failover, the reaper finds every lease expired relative to the new primary's clock. It processes expiries in
  bounded batches with jitter to avoid a thundering herd. Attempts whose workers are still alive are fenced; their
  results are rejected and the tasks are reassigned.
- **Durability contract:** production requires a synchronous standby (`synchronous_commit=on`,
  `synchronous_standby_names` with quorum 1) before claiming RPO 0 for committed transactions. Phase 1 local
  development has no HA: its RPO is the last backup (≤5 minutes) and its RTO is "restore and restart" (target 1h).
  This limitation is explicit; no document may claim otherwise.

### 3.7 Worker session takeover

- A worker ID must be unique. If a second process connects with the same ID, the newest `Hello` supersedes the old
  session; the old `session_id` is invalidated and its heartbeats reject. If the `boot_id` changed, all open
  attempts from the previous process are closed immediately as `lease_expired` (`outcome_unknown=true`) and handed
  to the retry policy. A reconnect with the same `boot_id` resumes only the attempts the worker lists as still
  active.
- This handles accidental duplicates and crashed pods with reused identity. It does **not** merge capacity; the
  worker manager replaces the session entry. Active leases from the old session remain until expiry and are then
  reaped.
- Production worker IDs derive from the deployment's stable identity (StatefulSet ordinal or registered node ID),
  not from a random value per reconnect.

### 3.8 Side-effect uncertainty

`effect_uncertain=true` means "we fenced the attempt, but the outside world may have changed". It is set when a
started attempt is abandoned (worker loss, cancellation of a running attempt, unreachable worker) and the node is
not `read_only`.

Downstream behaviour:

- The event and attempt record expose the flag; the dashboard shows a warning on the task.
- Automatic retry is allowed only for `read_only` nodes, `idempotent` nodes, or a node with an `idempotency_key`.
  Otherwise the task fails with `EFFECT_UNCERTAIN` and is dead-lettered for operator reconciliation.
- Compensation is a workflow concern, not a platform feature in Phase 1. The platform will not invent a rollback.

### 3.9 Delegation and MCP failures

- A supervisor loses its lease while a child runs: `GetDelegation` fails with `FAILED_PRECONDITION`; the parent
  attempt is fenced and retried. The child keeps its own execution and lease. The delegation idempotency key makes
  the retried supervisor tool call return the existing child instead of creating a duplicate.
- A child execution fails, is cancelled or times out: `GetDelegation` reports `failed` with the child's failure
  code. The supervisor decides whether to retry, use a fallback agent or fail. `delegate_tasks` supports a fallback
  retry for this case.
- The orchestrator dies after a child is created but before the delegation row commits: the child execution exists
  with `parent_execution_id` but no `delegations` row. The retried supervisor call can create a new child because
  the idempotency key was not committed; this is the one duplicate-child window and is bounded by `max_children`.
- An MCP server crashes or times out: the runtime returns a normalised tool error (`MCP_TIMEOUT`,
  `MCP_CONNECTION_CLOSED`, `MCP_HTTP_*`); the model sees the error and may retry within its turn budget. The
  connection is not reused after a dead process; the runtime process ends with the attempt.
- An MCP tool has side effects and the attempt crashes after the call: the tool's idempotency key is passed to the
  invocation; the MCP server is responsible for deduplication. The platform does not claim exactly-once external
  effects.
- Approval expiry releases the task on the next scheduler tick. The approval row is marked `expired`, an `expire`
  decision is audited, and `on_reject` decides whether the execution continues or fails.

## 4. Startup reconciliation

| Service | On startup |
|---|---|
| API | Connect DB, verify migrations/readiness, start HTTP. No state to rebuild. SSE connections are re-established by clients. |
| Orchestrator | Connect DB; acquire the recovery advisory lock (retry); rebuild the worker view as empty and wait for registrations (old workers become `unhealthy/terminated` via heartbeat timeout); start the publisher scanning the unpublished outbox; start the scheduler. It does not trust any prior in-memory assignment. |
| Worker | Load or generate stable ID; kill orphaned sandboxes and remove temp dirs; clear credential cache; connect gRPC; register; create/ensure its JetStream consumer; only then accept dispatch. It reports no result without a live lease. |
| Publisher | Claim `published_at IS NULL AND available_at <= now()` with `SKIP LOCKED`; publish; mark. It does not renumber or reorder state. |
| Recovery manager | Expire leases, force-reap tasks of terminated workers, repair active tasks with a missing attempt, enforce deadlines, repair interrupted executions with no active task, evaluate completion and report outbox lag in bounded batches. Idempotent on every row. |
| Dashboard | No state; fetches from API. |

## 5. Degraded modes

| Mode | Still works | Does not work | User-visible behaviour |
|---|---|---|---|
| NATS down | Submit, query, cancel, results of in-flight work, audit | New dispatches, live SSE | Executions stall in `pending`; UI says "scheduling delayed"; polling reflects committed state |
| Artifact volume down | Small results, inline checkpoints, state transitions | Large checkpoint offload, large artifacts, logs | Tasks requiring large state fail/retry; warning on checkpoints |
| Orchestrator down | API reads, auth, definitions | New submissions/cancels, scheduling, result commits | `503` on execution mutations; workers stop after lease TTL |
| Database read-only/failover | Nothing authoritative | Everything that needs writes; reads may continue briefly | Global degradation; `503` with retry |
| Worker fleet below capacity | State machine works | Work waits | `pending` tasks; queue age alert; no false failure |
| Dashboard down | API/CI continue | UI | Direct API use |

## 6. Backup and disaster recovery

| Asset | Backup | Target |
|---|---|---|
| PostgreSQL | Continuous WAL archiving + daily base backup; prod synchronous standby | RPO 0 with synchronous standby; RPO ≤5 min backup-only; RTO ≤1h Phase 1, ≤15 min managed |
| Checkpoint artifact volume | Snapshot with the host or use an S3/MinIO backend when multi-host | Loss can reduce resume granularity, never committed logical state |
| NATS | R3 stream storage in prod; not backed up | Loss never changes logical state; only delay |
| Definitions (`control`) | Included in PostgreSQL backup | Restored with DB |
| Secrets/KMS | KMS-managed, separate backup | Loss requires re-issuing credentials; not restorable from app DB |
| Config/manifests | Git | Redeploy |

Restore drill quarterly: restore DB to a point in time, start services, verify that terminal executions are
unchanged, non-terminal executions resume via reaper, and outbox drains. A restore that silently resurrects
in-flight work as successful is a failure of the drill.

## 7. Signals to alert on

- Outbox oldest unpublished age > 30s; publish error rate.
- Lease expiry rate and `WORKER_LOST` rate per pool.
- Result rejection rate (`STALE_LEASE`, `ALREADY_FINISHED`, `VALIDATION_FAILED`).
- Recovery manager last successful pass age; leases reaped per pass; executions repaired per pass.
- Worker heartbeats missing; registered vs active workers; dispatch consumer lag.
- Executions in `pending`/`assigned` with no state change beyond threshold.
- Execution deadline breaches and `EXECUTION_TIMEOUT` count.
- `effect_uncertain` tasks (should be rare and reviewed).
- Database replication lag, connection saturation, migration status.
- SSE connections, dropped connections, replay gaps.
- API 5xx/429 rates and idempotency conflicts.

## 8. Explicit non-recoveries

- External side effects already performed. No event replay, checkpoint or rollback undoes an HTTP call, payment or
  email.
- Python in-memory state not covered by the last committed checkpoint.
- Deleted or uncommitted object blobs (orphans are allowed to be garbage-collected).
- Published versions, executions or audit events purged by retention. Legal hold must be applied before retention
  would remove them.
- A terminal execution is never reopened. Recovery means starting a new execution with a new ID, linked in the
  audit trail by the operator, not mutating the old one.

## 9. Related decisions

- [ADR 0004 — at-least-once delivery](adr/0004-at-least-once-delivery.md)
- [ADR 0005 — task lease and heartbeat](adr/0005-task-lease-and-heartbeat.md)
- [ADR 0006 — idempotency and external side effects](adr/0006-idempotency-and-side-effects.md)
- [ADR 0012 — deployment topology](adr/0012-deployment-topology.md)
- [ADR 0014 — cancellation semantics](adr/0014-cancellation-semantics.md)

## Appendix A: Canonical failure codes

Codes are stable, machine-readable, and additive. A code says **what happened**, not whether it is retryable; the
node's `retry_on` policy and side-effect class decide that. `failure_code` on an execution is the first terminal
task failure, except for `EXECUTION_TIMEOUT`.

| Code | Layer | Meaning | Typical retry |
|---|---|---|---|
| `EXECUTION_TIMEOUT` | execution | Execution deadline reached; terminal `failed`. | No (new run) |
| `WORKER_LOST` | task/attempt | Lease expired or worker restart; outcome unknown. | Only if policy allows and side effects safe |
| `WORKER_BUSY` | attempt | Worker rejected a dispatch because it was at capacity or draining. | Yes, immediately on another worker |
| `EFFECT_UNCERTAIN` | task/attempt | A started side-effecting attempt was abandoned; external change may have occurred. | Only with declared idempotency |
| `LEASE_EXPIRED` | attempt | Lease ended without a valid result. | Per policy |
| `TASK_TIMEOUT` | attempt | Node timeout or reaper-enforced deadline. | Per `retry_on=timeout` |
| `RUNTIME_CRASH` | attempt | Python process exited abnormally / OOM. | Per `transient` |
| `RESULT_INVALID` | attempt | Malformed/oversized/ill-typed result. | Per policy, usually one retry then stop |
| `RESULT_SCHEMA_INVALID` | attempt | Result does not match the declared output schema. | Configurable (`retry_on=result_invalid`) |
| `CHECKPOINT_INVALID` | attempt | Hash/size/reference mismatch. | Per policy |
| `CHECKPOINT_INCOMPATIBLE` | attempt | ABI/schema/agent-version mismatch on resume. | No, unless restart is allowed |
| `CHECKPOINT_TOO_LARGE` | attempt | State exceeds the 32 MiB cap. | No |
| `CHECKPOINT_UPLOAD_FAILED` | attempt | Checkpoint upload failed; a success is never reported without durable state. | Per `transient` |
| `ARTIFACT_UPLOAD_FAILED` | attempt | Large output could not be stored. | Per `transient` |
| `ARTIFACT_TOO_LARGE` | attempt | Output/log exceeds the 64 MiB cap. | No |
| `PROTOCOL_ERROR` | attempt | Worker/runtime violated the contract repeatedly. | No; alert |
| `WORKER_CANCELLED` | attempt | Worker abandoned an attempt without a matching cancel request. | Per policy; treated as failure |
| `UNSUPPORTED_RUNTIME` | attempt/task | No worker supports the pinned ABI/image digest. | No until capacity changes |
| `EDGE_EVALUATION_FAILED` | task | CEL edge failed at runtime; terminal pre-execution failure. | No; fix the graph |
| `EDGE_EVALUATION_FAILED` | task | A CEL edge/condition/mapping could not be evaluated. | No; fix the graph |
| `INPUT_SCHEMA_INVALID` | task | Resolved node input does not match `input_schema`. | No |
| `INPUT_MAPPING_INVALID` | task | Edge data mapping expression failed. | No |
| `NODE_EXECUTION_FAILED` | task | A Go control node failed. | Usually no |
| `SUBWORKFLOW_FAILED` | task | A child execution failed or disappeared. | Per parent policy |
| `INTERNAL_ERROR` | any | Unexpected orchestrator error; state not committed. | Retry the operation |

