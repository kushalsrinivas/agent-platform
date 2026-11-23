# Service Responsibility Matrix

Normative for Phase 1. A deployable may only do what is listed here; everything else is a bug or an ADR.

## 1. Deployable summary

| | `api` | `orchestrator` | `worker` | `dashboard` |
|---|---|---|---|---|
| Language | Go | Go | Go + Python sandbox | TypeScript |
| Public ingress | Yes (HTTPS) | No | No | Yes (serves UI; calls API) |
| Holds authoritative state | No | Yes (execution) | No | No |
| Talks to PostgreSQL | Read-only (`execution`), read/write (`control`) | Read/write (`execution`), read-only (`control`) | No | No |
| Talks to NATS | Subscribe (SSE events) | Publish (outbox), subscribe (none in Phase 1) | Pull (own dispatch consumer) + publish to own DLQ | No |
| Talks to object storage | No | Presigned URL issuance, metadata | PUT/GET presigned blobs | No |
| Runs tenant code | No | No | Yes, sandboxed | No |
| Scaling unit | Request concurrency | Execution count / event throughput | Attempt concurrency / CPU | Static web tier |

## 2. `api` — external gateway and control plane

**Responsibilities**

- OIDC token verification, session handling, membership and role checks.
- Tenant resolution: derive `org_id` from the route + authenticated membership, never from request bodies.
- REST/SSE surface defined in [api/openapi.yaml](api/openapi.yaml).
- Create/read for projects; create, version and publish operations for agents and workflows (no content edit of
  any version).
- Validation at write time: JSON schema, CEL compilation, agent-version reference resolution, DAG acyclicity.
- Forward execution commands to the orchestrator over gRPC (`SubmitExecution`, `CancelExecution`).
- Serve execution queries by reading the `execution` schema with a read-only role.
- SSE fan-out: catch-up from `execution_events`, then tail NATS `events.org.<org>.>`; clients dedupe on event `seq`.
- HTTP rate limits, payload limits, CORS, request IDs, audit of write requests.

**Must not**

- Write execution/task/attempt/worker rows.
- Decide execution completion, retry, assignment or cancellation propagation.
- Accept a workflow version that is not published.
- Return `202` for an execution before the orchestrator confirms commit.
- Hold a queue of unsent commands as a source of truth.

**Control-plane writes are local transactions.** Creating an agent version or publishing a workflow is a single
PostgreSQL transaction in `control`; there is no outbox to execution. The orchestrator reads the committed row when
an execution starts. This is safe because definitions are immutable once referenced.

**Failure mode:** stateless; request fails cleanly. Retried execution submissions are deduplicated by
`request_id` at the orchestrator.

## 3. `orchestrator` — execution authority

**Responsibilities**

- Validate and commit execution submissions; materialise tasks and dependencies.
- Own both state machines: every transition in [execution](state-machines/execution.md) and
  [task](state-machines/task.md) happens here.
- Scheduler module: select eligible tasks and live workers, reserve a capacity slot through the worker-manager
  module, CAS assignment, create attempts and leases, write dispatch outbox rows.
- Worker-manager module: process registration/handshake, heartbeats, capacity view, lease renewals, drain, worker
  death detection.
- Lifecycle module: accept/reject fenced results, validate result shape, apply retry policy, evaluate completion,
  propagate cancellation, finalise executions.
- Reaper module: expire leases, recover orphaned attempts, finalise cancellations, enforce deadlines. Singleton via
  PostgreSQL advisory lock, safe to run on any replica.
- Event-publisher module: claim outbox rows, publish envelopes to NATS, mark published; retry with backoff.
- Persist checkpoints metadata and issue presigned upload/download URLs; never store blob bytes in PostgreSQL.
- Emit audit events and OpenTelemetry signals.
- Execute CEL edge expressions at task eligibility time.

**Must not**

- Run agent loops, call LLMs or tools, or execute tenant-supplied code.
- Mutate `control` definitions.
- Trust a worker's asserted state, retryability or completion.
- Publish a state change before committing it.
- Accept a result without a matching, unexpired lease.

**Internal composition**

```
orchestrator/
  lifecycle/       submission, result handling, completion, cancellation
  scheduler/       eligibility, placement, assignment CAS, dispatch outbox
  workermanager/   registry, heartbeat session, capacity, drain
  reaper/          lease expiry, deadline enforcement, cancellation finalisation
  publisher/       outbox claim/publish, NATS subjects
  store/           SQL, transactions, row locks, state-machine guards
  grpc/            worker control service
  events/          event construction, sequence, validation
```

Module boundaries are package boundaries in Phase 1. Cross-module writes go through the store layer so every
transition is auditable and testable, but they share one database transaction where correctness requires it.

**Failure mode:** any replica can serve any request. In-flight work is recovered by leases; uncommitted work never
happened; committed-but-unpublished outbox rows are published after restart.

## 4. `worker` — execution host

**Responsibilities**

- Register with a stable worker ID, boot ID, pool, labels, runtime ABIs, supported runtime image digests, capacity
  and version.
- Maintain the control stream: heartbeat (5s) with active `(attempt_id, lease_id)` pairs, accept/cancel/drain
  commands.
- Pull dispatch messages from its own durable JetStream consumer; reject messages for other workers/tenants.
- Start one isolated Python runtime process per attempt; pass task spec, tenant/project/execution context, deadline,
  resume checkpoint reference and task-scoped credentials.
- Enforce attempt deadline and cancellation: SIGTERM, grace period, SIGKILL; kill the whole process group.
- Upload checkpoints/artifacts through presigned URLs; verify SHA-256 before reporting.
- Report fenced results and errors; classify failure as `transient`/`permanent`/`timeout`/`cancelled` but never
  decide retry.
- Clean up sandbox directories, processes and secrets after every attempt and on startup.
- Emit logs/metrics/traces.

**Must not**

- Write PostgreSQL, publish lifecycle events, or call the API's internal endpoints.
- Hold leases after drain completes or after losing the orchestrator connection beyond the lease TTL.
- Report a result for an attempt it no longer owns.
- Persist task-scoped credentials on disk.
- Decide task success/failure or retry eligibility.

**Failure mode:** covered in [failure-and-recovery.md §3.3](failure-and-recovery.md#33-worker-crash-and-zombie-fencing).

## 5. Python runtimes — agent and tool execution

Both run inside a worker-owned sandbox. They are separate packages with separate entrypoints and one UDS gRPC
server per attempt.

| | `agent-runtime` | `tool-runtime` |
|---|---|---|
| Input | Agent version spec, execution input, upstream outputs, task params, checkpoint ref | Tool version spec, resolved arguments, tenant context, deadline |
| Work | Prompt assembly, model calls, agent loop, tool dispatch via local tool-runtime, checkpoint creation | HTTP/MCP/tool invocation, auth injection, output normalisation, timeout |
| Output | Output JSON, optional checkpoint, metrics | Output JSON, error classification, metrics |
| State | Its own internal state, serialised via the checkpoint contract (ADR 0008) | Stateless between attempts; MCP sessions are per attempt |
| Forbidden | DB/NATS access, deciding task completion, writing execution events, unbounded disk/network | Same |

Tool calls made **inside** an agent loop are not separate platform tasks and are not independently retried by the
orchestrator. The task attempt is the retry unit. This is why side-effect declarations apply at node level, not only
at tool level (ADR 0006).

## 6. `dashboard` — developer interface

- Next.js app; server components fetch from `api`; browser receives SSE through the API.
- No database, NATS, object-storage or orchestrator access.
- Read views: projects, agents + versions, workflows + versions, executions, tasks, attempts, events, worker fleet.
- Write views: create/edit drafts, publish, start execution, cancel execution. All mutations go through the API and
  carry the user's session.
- Optimistic UI is allowed only for drafts. Execution state is always server state; the dashboard never predicts a
  transition.

## 7. Capability decision matrix

| Decision | Decides | Executes | Persists | Observes |
|---|---|---|---|---|
| Create execution | orchestrator (validate + commit) | api (transport) | orchestrator | api/dashboard |
| Assign task | orchestrator scheduler | worker | orchestrator | orchestrator |
| Run node | orchestrator (dispatch) | worker + Python runtime | worker uploads blobs; orchestrator commits refs | OTel |
| Task result accepted | orchestrator | worker reports | orchestrator | dashboard |
| Retry task | orchestrator | scheduler/worker | orchestrator | dashboard |
| Execution complete | orchestrator | — | orchestrator | dashboard |
| Cancel execution | api requests; orchestrator commits `cancel_requested` | worker kills sandbox | orchestrator | dashboard |
| Worker healthy | worker-manager module | worker heartbeats | orchestrator (`workers`) | dashboard/ops |
| Publish workflow | api | — | api (`control`) | dashboard |
| Store checkpoint | worker uploads | Python serialises | object storage + orchestrator metadata | — |

## 8. Split triggers

The modules inside `orchestrator` stay there until one of these is true. "We might need it later" is not a trigger.

| Module | Split when | Extraction shape |
|---|---|---|
| Scheduler | Scheduling latency is dominated by orchestration load, or scheduling needs a different scaling curve than result handling. | Separate service with its own DB connection; assignment remains CAS on `execution.tasks`; dispatch via outbox. No new ownership. |
| Worker manager | Worker fleet spans regions/clusters and heartbeat volume burdens the orchestrator, or a separate ops control plane is required. | Service owning `workers` table; scheduler consumes a read model. Heartbeats never become authoritative elsewhere. |
| Event publisher | Outbox publish throughput or NATS fan-out exceeds one process, or independent delivery SLAs are required. | Stateless publisher replicas using `SKIP LOCKED`; safe today, so split is purely operational. |
| Agent/tool runtime | MCP session reuse, GPU scheduling, or untrusted BYO tools require a separate trust zone. | Independently deployed runtime service with the same UDS contract replaced by mTLS gRPC. Checkpoint/task semantics unchanged. |
| Control plane | Agent/workflow authoring grows into a separate product surface or needs independent deploys. | Split CRUD out of `api`; gateway remains transport/auth. Both keep `control` ownership boundaries. |

Splitting a module must not change: the state machine, the lease/fence model, the outbox, the message envelope, or
data ownership. If a split would change one of those, it needs an ADR.
