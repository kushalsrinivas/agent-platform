# Phase 08 — Durable execution, checkpointing and crash recovery

Status: complete. A multi-node execution survives a worker crash, resumes from its last valid checkpoint and
finishes exactly once logically. Duplicate delivery and stale workers cannot cause duplicate completion.

The exit criterion is met and tested against the live stack:

> A workflow task dispatched by Go executes a real LangGraph agent through a Python worker and returns a
> validated result to the Go orchestrator. A worker crash after a checkpoint resumes the same task from durable
> state, and a stale worker cannot commit a second result.

## 1. Checkpoint boundaries

A checkpoint is written only at a **safe execution boundary**, never per token or per provider chunk:

| Boundary | When | What is durable |
|---|---|---|
| Model response | After the model returns, before tools run or the node commits | Message history including the assistant response; a tool-call response resumes at the tool node with the same call IDs |
| Recoverable tool operation | Immediately after a tool call returns, before the next model turn | Message history including the tool result |
| Node completion | After the final model response with no tool calls, before the result is reported | Full state plus the completed output and usage |
| Human approval | Not applicable | No human nodes exist yet; the same sink would write before and after the wait |
| Agent-defined point | Not exposed | A future runtime adapter can call the sink at its own boundary |

The runtime writes each checkpoint as an atomic file in a private directory. The Go worker watches that directory
and uploads every file through the fenced `PutCheckpoint` RPC. A crash can therefore lose at most the in-flight
tool call, never the whole node. A workflow node may override the agent version with `checkpoint_mode: none` or
`inherit`, and `require_checkpoint: true` turns a failed encode/write into an attempt failure.

## 2. Checkpoint schema

`execution.checkpoints` records the durable envelope and its lineage:

| Field | Meaning |
|---|---|
| `id` | `ckpt_...`, assigned by the orchestrator |
| `execution_id`, `task_id`, `attempt_id`, `org_id` | Ownership and fenced attempt |
| `node_id`, `agent_version_id`, `workflow_version_id` | Pinned node and versions |
| `checkpoint_schema_version` | Envelope version (currently `1`) |
| `runtime_abi`, `runtime_version` | Producer compatibility |
| `parent_checkpoint_id` | Previous checkpoint for the task, derived by `created_at` |
| `sequence` | Monotonic within one attempt; makes retries idempotent |
| `object_key` **or** `state_inline` | Serialized state reference or inline bytes |
| `sha256`, `size_bytes` | Checksum and size |
| `created_at`, `expires_at` | Retention metadata |

The payload is gzip-compressed canonical JSON (`checkpointing.codec`):

```json
{
  "schema_version": 1,
  "agent_version_id": "agtv_...",
  "created_at": "2026-11-23T16:40:05Z",
  "state": {
    "messages": [["langchain", "..." ]],
    "steps": 2,
    "completed": true,
    "output": {"report": "...", "steps": 2, "model": "echo-1"},
    "input_tokens": 155,
    "output_tokens": 74,
    "model": "echo-1"
  }
}
```

### Serialization safety

- The runtime never uses pickle. Only gzip + JSON is accepted.
- The orchestrator validates before writing: gzip magic, bounded decompression, valid JSON, checksum match, schema
  version `1`, runtime ABI ≥ 1 and a size between 1 byte and 32 MiB.
- A checkpoint whose checksum or schema does not match is skipped in favour of an older readable checkpoint; it
  never fails the whole execution.
- `agent_version_id` and `workflow_version_id` must match the pinned task, so a checkpoint is never replayed into a
  different agent or workflow version.
- A schema change requires a new envelope version and an explicit migration or compatibility path. The current
  runtime rejects unknown versions instead of guessing.

### Large artifact offloading

- States up to **256 KiB** stay inline in PostgreSQL (`state_inline`) for fast recovery.
- Larger states go to the orchestrator's artifact store under
  `checkpoints/<execution_id>/<checkpoint_id>.json.gz`; only the object key is stored in PostgreSQL.
- Compose mounts the `checkpoint-artifacts` volume at `/var/lib/agent-platform/artifacts`. A zero-config bare
  orchestrator keeps all states inline up to 32 MiB.
- The store is deliberately behind two methods (`Put`, `Get`); an S3/MinIO backend can replace it without changing
  the lifecycle code. Workers never receive object-store credentials.

## 3. Upload and resume protocol

```
worker starts Python process
  → runtime writes ckpt-000001-<sha>.json.gz atomically
  → worker uploader reads the file, computes sha256
  → PutCheckpoint(task, attempt, lease, sequence, schema, abi, sha, bytes)
  → orchestrator fences the attempt, validates, stores, links parent
  → runtime returns
  → worker drains the uploader before reporting success
```

- `PutCheckpoint` is idempotent by `(attempt_id, sequence)`: re-uploading the same bytes returns the existing row;
  different bytes for the same sequence are rejected as `CHECKPOINT_CONFLICT`.
- A stale worker gets `CHECKPOINT_STALE_LEASE` and cannot write.
- If an upload fails permanently, the worker refuses to report success, so a success always has durable state.
- On the next attempt, `GetTaskContext` returns the newest readable checkpoint for the task, excluding the current
  attempt, with its schema version. The worker passes the bytes and version to `ExecuteNode`.
- The Python runtime restores the message history. A **completed** checkpoint returns the stored output without
  another model call; an intermediate checkpoint continues at the next graph step.

## 4. Recovery manager

`RunRecovery` replaces the standalone lease reaper loop and runs every `ORCHESTRATOR_REAPER_INTERVAL_MS`:

1. **Workers** — mark silent workers unhealthy, then terminated.
2. **Expired leases** — close the attempt, release the worker slot, apply the frozen retry policy and return the
   task to `pending`, or fail and dead-letter it.
3. **Terminated workers** — force-reap tasks whose worker row is already `terminated` instead of waiting for the
   lease clock. The fence still rejects a late result.
4. **Deadlines** — move due executions to `cancel_requested` with `cancel_reason=deadline`; quiescent deadlines end
   `failed` with `EXECUTION_TIMEOUT`.
5. **Dangling active tasks** — an active task whose latest attempt row is missing is replay-committed through the
   same lease decision instead of staying leased forever.
6. **Interrupted executions** — executions with no active task are re-evaluated to completion, so a restarted
   orchestrator converges instead of leaving them `running` forever.
7. **Outbox** — report unpublished rows; the separate publisher loop retries them. Recovery never invents or
   reorders events.

Recovery sequence for a worker crash:

```
worker dies
  → heartbeat expires → worker unhealthy → terminated
  → lease expires
  → recovery manager detects the task
  → verifies the execution is still schedulable
  → loads the latest valid checkpoint
  → applies the retry policy and side-effect safety
  → creates a new attempt with a new lease
  → dispatches to a live worker
  → runtime restores the checkpoint and resumes
```

## 5. Retry engine

The effective policy is frozen on `execution.execution_tasks.retry_policy` at materialisation. A later workflow
edit cannot change an in-flight execution.

| Field | Default | Meaning |
|---|---|---|
| `max_attempts` | 1 | Attempts including lease expiry |
| `retry_on` | `[transient, timeout, worker_lost]` | Allowed failure classes |
| `backoff_base_ms` | 1000 | First retry delay |
| `backoff_max_ms` | 60000 | Delay cap |

A workflow-level `policy.retry` is the default for nodes without their own `retry`; a node override wins. Backoff
is exponential with 0.5–1.0 jitter.

Failure handling:

| Failure | Classification | Behaviour |
|---|---|---|
| Provider outage, 5xx, transport | `transient` | Retry with backoff |
| Provider rate limit | `transient` | Retry with backoff |
| Node or execution timeout | `timeout` | Retry if `timeout` is listed |
| Worker lost or lease expired | `worker_lost` | Retry if the side effect is safe |
| Input/spec/auth failure | `permanent` | Terminal, dead letter |
| Output fails the node schema | `result_invalid` | Configurable retry (`retry_on: result_invalid`) |
| Cancelled run | `cancelled` | Never retry; cancellation is durable |
| Side effect outcome unknown | any + `effect_uncertain` | Retry only for `read_only`, `idempotent`, or a node with `idempotency_key`; otherwise terminal `EFFECT_UNCERTAIN` |

A terminal failure after the budget is exhausted sets `dead_lettered_at` and `last_failure_code`. Terminal
failures are the task-level dead-letter record; the admin messaging endpoint remains for NATS DLQ messages.

## 6. Idempotency

| Scope | Key | Enforcement |
|---|---|---|
| Workflow creation | API idempotency key | Existing control-plane idempotency table |
| Execution creation | `(org_id, request_id)` + request digest | Unique constraint; replay returns the same execution |
| Task attempt | `(task_id, attempt_number)`, unique `lease_id` | Database constraints + fence |
| Checkpoint upload | `(attempt_id, sequence)` + sha256 | `PutCheckpoint` idempotency |
| Tool call inside an agent | `execution_id:task_id:node_id:tool_call_id` | Passed to the tool; a resumed graph already contains the ToolMessage |
| Tool node / external side effect | `execution_id:task_id:node_id[:idempotency_key]` | Passed in `ToolTaskContext.idempotency_key`; the side-effecting tool must deduplicate |

A task attempt and an external side effect are not the same thing. If an attempt started a non-idempotent effect
and the worker disappeared, the platform records `effect_uncertain` and refuses an automatic retry. An operator
must reconcile before starting a new execution or manually retrying under a new idempotency key.

## 7. Cancellation and deadlines

- `POST .../executions/{id}:cancel` now calls the real orchestrator command. It is idempotent while the execution is
  non-terminal and returns `409` for `succeeded`, `failed` or `failing`.
- The cancellation decision is a committed transaction: `state=cancel_requested`, `cancel_epoch += 1`,
  `cancel_reason`, `cancel_requested_at`, `cancel_requested_by`, an audit event and cancelled pending tasks.
- After commit the orchestrator sends `CancelTask` to the live worker session. The worker marks the exact
  `(task, attempt, lease)` and cancels the attempt context; the Python process group receives `SIGTERM`, then
  `SIGKILL` after the grace period. A cancellation that arrived before the handler started is remembered and
  applied immediately.
- If the worker is offline, the lease expiry path closes the attempt as `cancelled` (or `lease_expired`) and the
  execution finalises. A restarted orchestrator sees `cancel_requested` and never restarts the work.
- A successful result that races a cancellation is recorded as cancelled; the execution cannot end `succeeded`.
- Execution deadlines use the same durable path with `cancel_reason=deadline`; a quiescent deadline ends `failed`
  with `EXECUTION_TIMEOUT`. Node timeouts are enforced by the worker deadline and the reaper.

## 8. Configuration

| Setting | Default | Meaning |
|---|---|---|
| `ORCHESTRATOR_ARTIFACT_DIR` | `./data/artifacts` | Checkpoint offload directory; Compose uses a named volume |
| `ORCHESTRATOR_REAPER_INTERVAL_MS` | `2000` | Recovery pass cadence |
| `ORCHESTRATOR_LEASE_SECONDS` | `30` | Lease TTL; recovery of a dead worker takes at most this plus one pass |
| `AGENT_RUNTIME_CHECKPOINT_DIR` | set by the worker | Private per-attempt checkpoint directory |
| `AGENT_RUNTIME_RESUME_MARKER` | unset | Test-only marker in resumed output |
| `AGENT_RUNTIME_TEST_*` | unset | Test-only pause/delay hooks; never set in production |

## 9. Recovery test suite

| # | Scenario | Test |
|---|---|---|
| 1 | Worker dies before receiving a task | `TestRecoveryWorkerDiesBeforeReceiving` |
| 2 | Worker dies after receiving a task | `TestRecoveryWorkerDiesAfterReceiving` |
| 3 | Worker dies during model generation | `TestRecoveryWorkerDiesDuringModelGeneration` |
| 4 | Worker dies after model response before result commit | `TestRecoveryResumesCompletedCheckpoint` |
| 5 | Worker dies after result commit before acknowledgement | `TestMessagingRedeliveryAndDuplicateSuppression` |
| 6 | Orchestrator restarts | `TestRecoveryOrchestratorRestart` |
| 7 | NATS restarts | `TestRecoveryNATSReconnect` |
| 8 | Database is temporarily unavailable | `TestRecoveryDatabaseUnavailable` |
| 9 | Lease expires while the old worker still runs | `TestWorkerManagerStaleWorkerCannotCommit` |
| 10 | Side effect completed, worker crashed before reporting | `TestRecoveryDoesNotRepeatCheckpointedToolEffect` |
| — | Execution deadline with no worker | `TestRecoveryExecutionDeadline` |

The checkpoint tests prove:

- attempt 1 ends `lease_expired`, attempt 2 ends `succeeded` and resumes the completed output with no new model call;
- a mid-run tool checkpoint resumes after the tool, the side-effect counter stays at one, and no checkpoint from the
  completed attempt is duplicated;
- a cancelled execution stays cancelled across an orchestrator restart and creates no attempts.

## 10. Deliberate deviations and follow-ups

| Item | Status | Reason / trigger |
|---|---|---|
| Object storage backend | Filesystem | The artifact store is behind `Put`/`Get`; Compose uses a durable volume. Replace with S3/MinIO when artifact volume size or multi-host orchestrators require it. |
| Human approval checkpoints | Not reachable | No human-in-the-loop node exists yet. The sink is at the correct boundary. |
| Agent-defined checkpoint API | Not exposed | Add when a runtime adapter needs finer granularity than tool/node boundaries. |
| Live checkpoint streaming to UI | Deferred | Checkpoints are durable; the dashboard can read the latest row. |
| Hard memory/CPU limits | Deployment concern | The runtime samples usage; cgroup/no-new-privileges/egress policy remains the sandbox phase. |
| Orchestrator-side output schema validation | Worker-side | The worker rejects invalid output before reporting; an authoritative orchestrator validator is still a follow-up. |
| Prepared-transaction side-effect ledger | Not implemented | Idempotency keys are passed to tools; a platform-managed ledger for arbitrary external systems needs a tool protocol and is a later slice. |
| Secrets for real providers | Deferred | The credential RPC is still an empty stub; only the deterministic echo provider runs end to end. |
