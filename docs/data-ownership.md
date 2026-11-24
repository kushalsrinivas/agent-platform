# Data Ownership

Normative for Phase 1. PostgreSQL is the source of truth; this document defines who may write what, how tenant
integrity is enforced, and how long data lives.

## 1. Physical layout

One PostgreSQL 16 cluster, one database `agent_platform`, two schemas:

| Schema | Owner | Purpose |
|---|---|---|
| `control` | `api` (Go control plane) | Organizations, projects, memberships, agents/versions, workflows/versions, credentials, HTTP idempotency. |
| `execution` | `orchestrator` (Go) | Executions, tasks, dependencies, attempts, leases, small checkpoints and checkpoint metadata, workers, event history, outbox. |

Observability data is **not** in PostgreSQL. Traces, metrics and service logs go to the OTel backend; bulky task
logs go to object storage. Nothing in observability is used to make a correctness decision.

Splitting into separate physical databases later is possible along the same schema boundary. Cross-schema foreign
keys are intentionally **not** used; the boundary is enforced by roles and validated references (see §4).

## 2. Roles and grants

| Role | `control` | `execution` | Used by |
|---|---|---|---|
| `app_api` | `SELECT, INSERT, UPDATE, DELETE` | `SELECT` on executions/tasks/attempts/checkpoints/events/workers; **no** `outbox` | `api` |
| `app_orchestrator` | `SELECT` (definitions + credentials) | `SELECT, INSERT, UPDATE, DELETE` on mutable tables; `SELECT, INSERT` only on `execution_events` | `orchestrator` |
| `app_retention` | — | `DELETE` on execution tables after retention checks | retention job |
| `app_migrate_control` | DDL | — | control migration job |
| `app_migrate_execution` | — | DDL | execution migration job |
| `app_readonly_ops` | `SELECT` | `SELECT` | break-glass/ops only |

- No role owns both schemas. Migrations run in dependency order (`control`, then `execution`).
- Workers have **no** database credentials at all.
- The orchestrator's role may have `BYPASSRLS` because scheduling is intentionally cross-tenant. Every such query
  must still filter or aggregate by `org_id` where it touches one tenant's data; fleet queries (scheduler scan,
  worker reaping) are reviewed as a small, enumerated list. The API role is RLS-bound and never bypasses.
- Human access is read-only through `app_readonly_ops`, audited by the database provider. Application roles are
  not used interactively.

## 3. Tables

### 3.1 `control` schema

| Table | Key columns and constraints | Write rules |
|---|---|---|
| `organizations` | `id pk, name, slug unique, status, created_at, updated_at` | API only. |
| `projects` | `id pk, org_id fk, name, slug, status, created_at, updated_at, unique(org_id, slug)` | API only. Soft delete. Deletion blocked while non-terminal executions exist. |
| `memberships` | `org_id, principal_id, role ∈ {owner, operator, viewer}, unique(org_id, principal_id)` | API only. Phase 1 roles are org-wide; project-level ACLs are deferred. |
| `agents` | `id pk, org_id, project_id, name, description, latest_version, timestamps, unique(project_id, name)` | API only. `latest_version` is a convenience pointer; never used for execution. |
| `agent_versions` | `id pk, agent_id, org_id, project_id, version int, status ∈ {draft, published, deprecated}, digest, spec jsonb, runtime_abi, created_by, created_at, published_at, unique(agent_id, version), unique(agent_id, digest)` | Insert by API. Spec/digest are immutable from the first write; only status transitions are allowed (`draft → published → deprecated`). No update path for content, no delete. |
| `environments` | `id pk, org_id, project_id fk, name, slug, kind ∈ {development, staging, production}, timestamps, unique(project_id, slug)` | API only. Labels/metadata; executions pin versions, not environments. |
| `api_keys` | `id pk, org_id, project_id?, name, key_prefix, key_hash unique, role ∈ {viewer, operator, owner}, created_by, created_at, last_used_at, expires_at, revoked_at` | API owner only. Hash only; plaintext shown once. Revocation is immediate and removes the key principal's membership. |
| `workflows` | `id pk, org_id, project_id, name, description, latest_version, timestamps, unique(project_id, name)` | API only. |
| `workflow_versions` | `id pk, workflow_id, org_id, project_id, version int, status ∈ {draft, published, deprecated}, digest, graph jsonb, created_by, created_at, published_at, unique(workflow_id, version), unique(workflow_id, digest)` | Insert by API. Graph/digest are immutable from the first write; only status transitions are allowed. Publish is a one-way transition with validation and no content update path. |
| `credentials` | `id pk, org_id, project_id, name, ciphertext bytea, nonce bytea, kms_key_id, created_by, created_at, rotated_at, unique(project_id, name)` | API writes (write-only values). Orchestrator reads only to issue task-scoped credentials. Never returned by any read API. |
| `idempotency_keys` | `org_id, key, principal_id, method, path, request_hash, response_status, response_body jsonb, resource_id, created_at, expires_at, pk(org_id, key)` | API only. Used for non-execution POST replay; execution dedupe is `execution.executions.request_id`. |

Control-plane writes are single-schema transactions. There is deliberately no outbox: definitions are immutable
once referenced, so the orchestrator reading the committed row at execution start is sufficient.

### 3.2 `execution` schema

| Table | Key columns and constraints | Notes |
|---|---|---|
| `executions` | `id pk, org_id, project_id, workflow_id, workflow_version_id, state, state_version bigint, request_id, request_digest, input jsonb (≤256 KiB), input_digest, created_by, created_at, started_at, ended_at, deadline_at, cancel_epoch int, cancel_reason, failure_code, failure_detail, next_event_seq bigint, parent_execution_id?, parent_task_id?, iteration int, replay_of_execution_id?, delegation_policy jsonb, delegation_depth int, unique(org_id, request_id)` | One row per run. `state_version` increments on every transition. `next_event_seq` allocates audit sequence under the row lock. Child executions link to the parent task; replay link is audit-only. `delegation_policy` is the child/supervisor's frozen orchestration budget; `delegation_depth` is capped at 32. |
| `execution_tasks` | `id pk, execution_id fk, org_id, node_id, node_type (start/end/agent/llm/tool/condition/transform/parallel/join/delay/approval/subworkflow/loop), agent_version_id?, tool_version_id?, worker_pool?, state, state_version, attempt_count, max_attempts, next_attempt_at, owner_worker_id?, lease_id?, lease_expires_at?, latest_attempt_id?, skip_reason?, side_effect_class, started_at, ended_at, config jsonb, input_from jsonb, input_schema jsonb, output_schema jsonb, params jsonb, resources jsonb, join_policy jsonb, control_state jsonb, result_inline jsonb, unique(execution_id, node_id)` | Materialised once per execution. CHECK ties owner/lease presence to `assigned`/`running`; control nodes carry no agent/tool version. Partial index on `(next_attempt_at) WHERE state='pending'`. |
| `task_dependencies` | `execution_id, task_id, depends_on_task_id, when_expr text?, when_digest, data_mapping jsonb, priority int, org_id, pk(task_id, depends_on_task_id)` | Static graph edges. CEL conditions and data mappings are validated at publish and evaluated by the scheduler against upstream outputs. |
| `task_attempts` | `id pk, task_id fk, execution_id, org_id, attempt_number int, worker_id, lease_id uuid unique, outcome ∈ {succeeded, failed, cancelled, lease_expired, incomplete}, outcome_unknown bool, effect_uncertain bool, dispatched_at, accepted_at, started_at, finished_at, error_code, error_message, result_inline jsonb?, result_object_key?, result_sha256?, result_size_bytes?, result_content_type?, checkpoint_id?, metrics jsonb, runtime_version, unique(task_id, attempt_number)` | Partial unique index `(task_id) WHERE finished_at IS NULL` enforces at most one open attempt. Inline results are nulled after retention. Artifact hash/size/type are stored with the object key so the result can be validated before commit. |
| `checkpoints` | `id pk, execution_id, task_id, attempt_id, org_id, node_id, workflow_version_id, parent_checkpoint_id, sequence, object_key?, state_inline?, sha256, size_bytes, checkpoint_schema_version, runtime_abi, runtime_version, agent_version_id, idempotency_key?, created_at, expires_at` | Small states are inline; larger states are offloaded to the artifact store. Exactly one of `object_key` and `state_inline` is set. Unique `(attempt_id, sequence)`. The orchestrator commits the reference through the fenced `PutCheckpoint` RPC. |
| `delegations` | `id pk, org_id, root_execution_id, parent_execution_id, parent_task_id, child_execution_id unique, agent_id, agent_version, request jsonb (≤256 KiB), shared_context jsonb (≤256 KiB), context_refs text[], status ∈ {pending, running, succeeded, failed, cancelled}, result_inline jsonb?, error_code?, error_message?, retries int, idempotency_key text, created_at, finished_at, unique(parent_task_id, idempotency_key) WHERE idempotency_key <> ''` | One row per supervisor delegation. `root_execution_id` scopes budgets across the tree. Child executions also carry `parent_execution_id`/`parent_task_id`. |
| `agent_events` | `id pk, org_id, root_execution_id, execution_id, task_id?, topic, payload jsonb, created_at, consumed_by_execution_id?, consumed_at?` | Durable topic messages inside one execution hierarchy. Partial index on unconsumed `(root_execution_id, topic, created_at)`; consumers use `FOR UPDATE SKIP LOCKED`. |
| `approvals` | `id pk, org_id, project_id, execution_id, task_id unique, prompt, policy ∈ {any, all}, approvers text[], status ∈ {pending, approved, rejected, expired}, requested_at, expires_at?, decided_at?, decided_by?, reason?` | One durable row per approval task. Waiting approvals have pending tasks with `control_state` and `next_attempt_at`; no worker slot is reserved. |
| `approval_decisions` | `id pk, approval_id, actor, decision ∈ {approve, reject, expire}, comment, created_at` | Immutable vote audit. Deleted only with the parent approval. |
| `execution_events` | `id bigserial, event_id text unique, execution_id, org_id, seq bigint, event_type, entity_type, entity_id, actor_type ∈ {user, system, worker}, actor_id, from_state?, to_state?, reason?, payload jsonb, trace_id?, created_at, unique(execution_id, seq)` | Append-only audit. A trigger rejects UPDATE/DELETE for the orchestrator role; only `app_retention` may delete after the horizon. `seq` is per execution, allocated from `executions.next_event_seq` under the execution row lock. |
| `outbox` | `id uuid pk, message_id unique, subject, message_type, schema_version int, org_id, execution_id?, payload jsonb, headers jsonb, created_at, available_at, published_at?, attempts int, last_error?` | Claimed with `FOR UPDATE SKIP LOCKED`; published rows retained briefly for debugging (ADR 0013). |
| `workers` | `id text pk, boot_id uuid, pool, status ∈ {active, unhealthy, draining, offline}, max_concurrency int, reported_available_slots int, reserved_slots int, labels jsonb, runtime_abis text[], runtime_digests text[], binary_version, protocol_version, session_id?, registered_at, last_heartbeat_at, drained_at?, created_at, updated_at` | Fleet registry. Not tenant-scoped. Status is derived from heartbeats and drain commands, never asserted by the worker. A changed `boot_id` invalidates the previous process's open attempts. `reserved_slots` is incremented transactionally at assignment and released exactly once when the attempt closes; availability = min(reported, max − reserved). |

## 4. Referential integrity across the boundary

- Within `execution`, foreign keys and CHECK constraints are mandatory.
- Across `control` → `execution`, the orchestrator validates on submission and materialisation:
  - workflow version exists, is `published` (a deprecated workflow version accepts no new executions), and belongs
    to the route `org_id`/`project_id`;
  - every node's agent/tool version exists, is `published` or was deprecated after the workflow version pinned it,
    and belongs to the same project. Deprecation blocks new workflow publication; it does not break a pinned graph;
  - graph is acyclic, CEL compiles, versions pin exact IDs;
  - execution input conforms to the workflow's input schema and size limit.
- The execution stores `workflow_version_id` and the task stores the exact `agent_version_id`/`tool_version_id`, so
  audit does not depend on a live join. A nightly integrity job checks for dangling references and alerts; it never
  deletes execution data.

## 5. Tenant isolation

- Every tenant-owned table carries `org_id NOT NULL`. Composite uniqueness and indexes put `org_id` first.
- Row-level security is enabled on `executions`, `execution_tasks`, `task_attempts`, `task_dependencies`,
  `checkpoints`, `execution_events`, `outbox`, and all `control` tables. Policy:
  `org_id = current_setting('app.tenant_id', true)::text`.
- The owning service sets `SET LOCAL app.tenant_id = $1` at the start of each transaction that serves one tenant.
  The API's read-only execution queries do this on every request. Missing setting means no rows, not all rows.
- Organisation discovery (`GET /v1/orgs`) is the only query before tenant context. It calls a narrowly scoped
  `SECURITY DEFINER` function that takes `app.principal_id` and returns only `(org_id, name, slug, role)` for that
  principal's memberships. It cannot read projects, definitions or execution data. Every subsequent request sets
  the tenant and uses normal RLS.
- The scheduler's fleet scan uses the documented BYPASSRLS orchestrator role. Its queries enumerate
  `(org_id, execution_id, task_id)` and any subsequent single-tenant transaction sets `app.tenant_id` even though
  the role could bypass it. This is tested.
- Object keys and NATS subjects carry `org_id`. Cross-tenant access is rejected server-side at the API, at
  presigned-URL issuance, and at the worker's dispatch filter.

## 6. Artifact storage

Checkpoint layout under `ORCHESTRATOR_ARTIFACT_DIR` (a filesystem volume locally, S3/MinIO behind the same
`Put`/`Get` interface in production):

```
checkpoints/<execution_id>/<checkpoint_id>.json.gz
outputs/<artifact_id>.json
logs/<attempt_id>.log
```

Rules:

- Objects are immutable. A retry writes a new checkpoint ID; it never overwrites a committed checkpoint.
- The orchestrator is the only reader and writer. Checkpoint bytes are produced by Python, uploaded by the Go
  worker over the fenced gRPC RPC, and stored by the orchestrator. Workers and Python never hold object-store
  credentials.
- The database stores the object key, SHA-256, size and schema/runtime versions. States ≤256 KiB stay in
  `state_inline`; larger states are offloaded. A blob without a committed row is an orphan and may be garbage
  collected.
- Artifacts are content-addressed by a random ID plus SHA; duplicate uploads are harmless but the reference is
  committed once.

## 7. Retention and deletion

| Data | Default retention | Mechanism |
|---|---|---|
| Executions, tasks, attempts (metadata) | 13 months after terminal | Batched deletes indexed by terminal time. Monthly partitioning is a later optimisation and must include the partition key in keys/constraints. |
| `execution_events` (audit) | 13 months after terminal | Same job, same horizon. Legal hold blocks deletion. |
| Inline attempt results | While execution is non-terminal, then 30 days after attempt finish | `UPDATE ... SET result_inline = NULL`, keep row. |
| Checkpoints | While the execution is non-terminal, then 30 days / latest 2 per task | Metadata tombstone plus artifact cleanup. |
| Artifacts | While the execution is non-terminal, then 30 days | Artifact cleanup. |
| Task logs | While the execution is non-terminal, then 14 days | Artifact cleanup. |
| Outbox published rows | 7 days | Delete job. |
| `workers` rows | 7 days after `offline` | Delete job; attempts keep the worker ID. |
| Idempotency keys | 24h after linked execution terminal, max 7 days | Delete job. |
| Published agent/workflow versions | Indefinite in Phase 1 | No delete path; soft-delete parents. Needed for audit. |
| NATS `TASK_DISPATCH` | 24h | Stream limits. |
| NATS `EVENTS` | 72h | Stream limits. |

Retention is configuration with safe bounds; lowering it below 30 days for audit events requires an ADR. A deletion
job must never remove an execution in a non-terminal state; cancel/timeout finalisation precedes retention.

## 8. Migration ownership

- Each schema has its own ordered migration directory and role: `migrations/control/`, `migrations/execution/`.
- A migration may only touch its own schema. Cross-schema data changes are application migrations executed by the
  owning service, not SQL run by the other.
- Expand/contract: add nullable columns and backfill before enforcing `NOT NULL`; create indexes concurrently;
  drop only after two releases. Down migrations are not relied on in production; roll forward.
- State enums are stored as `text` with CHECK constraints (not native enums) to make additive changes cheap and
  transactional. Adding a state requires an ADR and a coordinated deploy.
- The schema is validated in CI by applying all migrations to a clean database and running the state-machine
  constraint tests.

## 9. Prohibited patterns

- Python or the worker writing any table.
- The API writing `execution.*`; the orchestrator writing `control.*`.
- Deriving task or execution state from `execution_events` or NATS. Events are audit and fan-out.
- Storing large checkpoint bytes, logs or large results in PostgreSQL. Small checkpoint inline storage (≤256 KiB)
  is intentional so recovery works without the artifact volume.
- Deleting or editing a published version.
- Using worker-clock timestamps for lease expiry or ordering.
- Unscoped queries in tenant-facing code paths.
