# Phase 03 — Data model and shared contracts

**Status:** implemented · **Depends on:** [data-ownership.md](data-ownership.md), [ADR 0007](adr/0007-workflow-graph-representation.md),
[ADR 0008](adr/0008-checkpoint-serialization.md), [ADR 0009](adr/0009-agent-versioning.md), [communication.md](communication.md)

This phase turns the Phase 01 design into an executable schema and versioned contracts. The Phase 01 documents
remain normative; this page records what shipped, how the phase brief maps to it, and what is deliberately deferred.

## 1. What shipped

| Area | Artefact |
|---|---|
| Control schema | `migrations/control/0002_tables`, `0003_security` — 9 tables, RLS, immutability triggers, grants. |
| Execution schema | `migrations/execution/0002_tables`, `0003_security` — 8 tables, lease/attempt constraints, append-only events, outbox, RLS. |
| Workflow contract | `packages/schemas/workflow.schema.json`, Go and Python models, six fixtures. |
| Task contracts | `task-dispatch.schema.json`, `task-result.schema.json`, Go/Python models, success and failure fixtures. |
| Event contracts | Envelope from `docs/api/envelope.schema.json` plus five payload schemas and typed parsers. |
| Canonical digests | RFC 8785 JSON Canonicalization Scheme; identical graph/document digests in Go and Python. |
| Outbox | `execution.outbox`, `internal/outbox` store, claim/mark methods and a `Publisher` contract. |
| Proto | `TaskProgress` added to the worker control stream; all other operations already existed. |
| Instrumentation | pgx slow-query/error tracer (no parameter values logged) and `postgres.WithTx`. |

## 2. Schema at a glance

### `control` (API writes, orchestrator reads)

| Table | Purpose |
|---|---|
| `organizations` | Tenant root. |
| `projects` | Work scoping; soft delete only. |
| `memberships` | OIDC principal → organisation role. There is no local `users` table. |
| `agents`, `agent_versions` | Definition + insert-only version rows with content digest and runtime ABI. |
| `workflows`, `workflow_versions` | Definition + insert-only graph version. The graph is JSONB. |
| `credentials` | Envelope-encrypted secret values, referenced by name. |
| `idempotency_keys` | Replay protection for non-execution POSTs. |

### `execution` (orchestrator writes, API reads)

| Table | Purpose |
|---|---|
| `executions` | One row per run, state/version, immutable input, event sequence, fencing-relevant fields. |
| `execution_tasks` | One row per graph node, attempt budget, owner/lease, skip reason, side-effect class. |
| `task_dependencies` | Immutable graph edges materialised per execution, including CEL source/digest. |
| `task_attempts` | Every dispatch, independently identified, closed once, never rewritten. |
| `checkpoints` | Metadata for immutable checkpoint blobs; bytes live in object storage. |
| `execution_events` | Append-only per-execution audit with gap-free `seq`. |
| `outbox` | Durable publication rows committed with state changes. |
| `workers` | Fleet registry: capacity, capabilities, heartbeat, drain state. Not tenant-scoped. |

## 3. Phase brief → implementation mapping

The brief is a menu, not all of it is in the accepted Phase 1 architecture. This table is the honest mapping.

| Brief | Implemented as | Note |
|---|---|---|
| `users` | `memberships.principal_id` | Authentication is OIDC; the platform never stores passwords or profiles. |
| `organizations` | `control.organizations` | |
| `organization_members` | `control.memberships` | Org-wide roles only in Phase 1. |
| `projects` | `control.projects` | |
| `environments` | — | Project is the only scoping level in ADR 0002/0007. Deferred until a concrete environment feature exists. |
| `api_keys` | — | Authentication is OIDC in Phase 1. API keys need an auth design/ADR. |
| `secrets` | `control.credentials` | Envelope-encrypted values, names only on read. |
| `agents`, `agent_versions` | Same names | Version content immutable after insert; status transitions only. |
| `agent_deployments` | — | Versions are pinned by workflow versions; there is no deployment object in the architecture. |
| `workflows`, `workflow_versions` | Same names | |
| `workflow_nodes`, `workflow_edges` | `workflow_versions.graph` JSONB | One immutable source of truth. The graph is validated by the shared contract before insert. |
| `executions` | Same name | `tenant_id` → `org_id`, `current_state_version` → `state_version`, `idempotency_key` → `request_id`, `output_reference` → attempt outputs/artifacts. |
| `execution_nodes`, `tasks` | `execution_tasks` + `task_attempts` | One task per node; attempts are separate rows. No dynamic fan-out in Phase 1. |
| `execution_outputs` | `task_attempts.result_inline/result_object_key` + `checkpoints` | Inline outputs (≤64 KiB) are required for success; large data is an artifact reference. |
| `workers` | Same name | |
| `worker_capabilities` | `workers.runtime_abis`, `runtime_digests`, `labels` | Small inline arrays; no separate table until capability history is needed. |
| `worker_heartbeats` | `workers.last_heartbeat_at` | Heartbeats are high-volume and only the latest value has scheduling value. Fleet history belongs in metrics. |
| `queue_assignments` | `execution_tasks.owner_worker_id/lease_*` + `workers.reserved_slots` | Assignment is the task row; there is no separate queue table. |
| `tool_definitions`, `tool_connections`, `mcp_servers` | — | Phase 1 has platform-provided, versioned tools only; a tenant tool registry/MCP is an explicit non-goal. MCP descriptors exist in the Python runtime for the later transport. |
| `approvals` | — | No approval state in the execution/task state machines. Human-in-the-loop needs an ADR and state-machine changes. |
| `usage_records`, `provider_usage` | `task_attempts.metrics` | Token/duration telemetry is stored per attempt; billing-grade analytics is a later pipeline. |
| `audit_logs` | `execution_events` | Execution audit is complete. Control-plane audit needs its own event design. |
| `outbox_events` | `execution.outbox` | |

Adding any deferred table requires an ADR first, because each one changes an ownership boundary or a Phase 1
non-goal.

## 4. Invariants encoded in SQL

- **Tenant scope** — `org_id` on every tenant table, `org_id` first in indexes and uniqueness constraints. RLS
  compares `org_id` (or `organizations.id`) with `current_setting('app.tenant_id', true)`. No setting means no rows.
  `app_orchestrator`, `app_retention` and `app_readonly_ops` have `BYPASSRLS` by design; `app_api` does not.
- **Org discovery** — `control.list_orgs_for_principal()` is `SECURITY DEFINER`, reads only rows for
  `app.principal_id`, and is the only pre-tenant query.
- **Version immutability** — a trigger rejects any change other than `draft → published → deprecated`; content and
  digest cannot be edited. Definitions are soft-deleted, so the API has no `DELETE` grant on them.
- **Materialised shape** — a task exists before any worker is assigned; `UNIQUE (execution_id, node_id)` and
  `UNIQUE (task_id, attempt_number)` make duplicate materialisation visible.
- **Lease/fence shape** — `assigned`/`running` tasks require owner, lease and expiry; every other state requires
  them to be null. A partial unique index allows one open attempt per task and `lease_id` is globally unique.
- **Audit integrity** — `execution_events` is insert-only for the orchestrator and rejected for `UPDATE` by a
  trigger; `(execution_id, seq)` is unique. `executions.next_event_seq` is allocated under the execution row lock
  in application code.
- **Attempt immutability** — once `finished_at` is set, only `result_inline` may be cleared by retention.
- **Outbox consistency** — the table is written in the same transaction as the state change; claim uses
  `FOR UPDATE SKIP LOCKED`; published rows are marked, failed rows are rescheduled with attempts/last_error.
- **Outcome checks** — state enums are `text` + `CHECK`; terminal executions require `ended_at`; failures require a
  failure code; task skips require a reason.
- **Retention-compatible FKs** — execution cascades own its tasks/attempts/checkpoints/events/outbox; control
  definitions are `RESTRICT` until explicitly cleaned in dependency order.

## 5. Workflow contract

File: `packages/schemas/workflow.schema.json` (`schema_version: 1`).

- Nodes are `agent` or `tool`; **there are no start/end nodes**. Roots (no inbound edges) always run; terminals are
  derived. This follows [ADR 0007](adr/0007-workflow-graph-representation.md); the brief's `start`/`end` example is
  illustrative and was not adopted.
- Agent and tool references carry exact integer versions. `latest` is impossible by type and rejected.
- `input_from` may only reference upstream nodes; the validator computes ancestors and rejects other references.
- Edges may carry a CEL `when` string. Compilation is a publish-time control-plane check, not part of the shared
  validators; both languages only enforce presence, length and graph shape.
- Non-idempotent nodes with `max_attempts > 1` require `idempotency_key` (ADR 0006).
- Limits: 1..500 nodes, ≤2000 edges, ≤16 `input_from` references, node IDs `[a-z][a-z0-9_]{0,63}`.

**Canonicalisation.** Digests use RFC 8785 (JCS) over the *graph* (nodes, edges, policy, input_schema). Absent
optional fields are omitted rather than defaulted; defaults are applied at execution time. The same fixture yields
the same digest in Go and Python, asserted against `packages/test-fixtures/workflow.digests.json`.

## 6. Task and event contracts

- `task-dispatch.schema.json` is the self-contained attempt descriptor a worker validates before accepting.
- `task-result.schema.json` is the fenced result. Success must carry an inline output object (may be empty) and no
  failure; failed must carry a failure; unknown classifications are rejected.
- Events use `docs/api/envelope.schema.json` (envelope v1) and one payload schema per message type:
  `execution.created`, `execution.state_changed`, `task.state_changed`, `task.attempt_finished`, `task.dispatch`.
- Task assignment is a NATS work-queue dispatch, not a gRPC call: the assignment is committed first, the message
  only points the worker at the committed attempt, and the worker accepts it over the gRPC control stream. This is
  deliberate ([communication.md §6](communication.md#6-nats-jetstream)); replacing it with server-push gRPC would
  duplicate the assignment state machine.
- `TaskProgress` was added to the worker proto. It is advisory telemetry and never a state transition.

**Deferred event types.** `execution.paused/resumed` are not states in the Phase 1 execution machine. Approval and
worker-fleet events are deferred with their features; `checkpoint.created` is represented by the checkpoint
reference on `task.attempt_finished` and by `execution_events` rows, not published as a separate message. Adding
these types is additive and requires the corresponding domain feature, not just a schema.

## 7. Outbox contract

`internal/outbox` provides:

- `NewMessage(...)` — UUIDv7 row ID, stable `msg_...` message ID, JSON object payload, schema version 1.
- `Store.Insert` — must be called inside the transaction that changes state.
- `Store.Claim` — `FOR UPDATE SKIP LOCKED`, bounded batch, due rows only.
- `Store.MarkPublished` / `MarkFailed`.
- `Publisher` interface and `DispatchOnce` — a working at-least-once loop. It intentionally holds the claim
  transaction while publishing; Phase 05 may add a claim token to shorten the lock. Duplicate publishes after a
  crash are allowed and consumers are idempotent; `MessageID` maps to the broker idempotency key.

## 8. Migrations, pooling and instrumentation

- Apply `control` then `execution`. Every migration has a down file, but down migrations are development-only;
  production uses expand/contract and roll-forward ([data-ownership.md §8](data-ownership.md#8-migration-ownership)).
- `postgres.Connect` configures a pool. `postgres.WithTx` is the transaction helper; the state-machine documents
  define the only permitted lock order (execution, then task).
- The pgx tracer logs queries slower than 200 ms and all failures with SQL text, never parameter values.
- JSONB GIN indexes exist on `workflow_versions.graph` and `execution_events.payload`; tenant-scoped B-tree
  indexes cover list, scheduler, lease and outbox scans.

## 9. Verification

```bash
make integration-test        # migrates the Compose DB, then runs schema/RLS/outbox tests
go test ./packages/contracts # JSON Schema + semantic validation + digests
cd runtimes/python && uv run pytest tests/test_contracts_*.py
make lint && make test
```

The integration suite proves: RLS with/without tenant, org discovery, read/write role separation, duplicate
idempotency rejection, lease/attempt constraints, terminal-state checks, version immutability, append-only events,
closed-attempt immutability, and outbox claim/mark/skip-locked behaviour.

## 10. Deferred, with triggers

| Deferred | Trigger to revisit |
|---|---|
| `environments` | A feature needs environment-specific configuration or promotion. || `api_keys` | A non-OIDC client authentication path is required. |
| `agent_deployments` | Rollout/alias semantics are requested beyond exact version pinning. |
| Normalized workflow node/edge tables | Graph querying at scale or per-node permissions; needs an ADR to avoid two sources of truth. |
| Tenant tools/MCP registry, approvals | The corresponding Phase 1 non-goals are lifted by an ADR. |
| Usage/billing tables | Billing-grade metering or provider invoicing is required. |
| Control-plane audit log | A control-plane audit event model is specified. |
| CEL compilation in shared validators | Publish-time validation is implemented in the control plane; Python never evaluates CEL. |

## 11. Known follow-up

The runtime checkpoint *bytes* still use the Phase 02 gzip/state-schema scaffold. [ADR 0008](adr/0008-checkpoint-serialization.md)
specifies a zstd envelope with runtime and execution metadata. The database, task-result and proto contracts
already carry the object key, hash, size, checkpoint schema version and runtime ABI, so aligning the Python codec
is a runtime change with no migration. Do it with the worker/runtime ABI implementation in Phase 04.
