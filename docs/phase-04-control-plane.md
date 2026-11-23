# Phase 04 — Go API and control plane

**Status:** implemented · **Depends on:** [architecture.md](architecture.md), [data-ownership.md](data-ownership.md),
[phase-03-contracts.md](phase-03-contracts.md), [ADR 0006](adr/0006-idempotency-and-side-effects.md),
[ADR 0007](adr/0007-workflow-graph-representation.md), [ADR 0009](adr/0009-agent-versioning.md)

The API is now a working control plane: an authenticated principal can create an organisation and project, register
and publish an agent, create and publish a workflow, and submit an execution that is durably committed with tasks,
an audit event and an outbox row. No worker execution is started; tasks remain `pending`.

## 1. Authentication

Phase 1 authenticates with **OIDC bearer JWTs**, as required by [architecture.md §10](architecture.md#10-security-and-tenancy).

| Concern | Where it lives |
|---|---|
| Registration, login, logout, password hashing, token rotation | The OIDC identity provider. The platform never stores passwords or local user rows. |
| Request authentication | `services/api/internal/auth` verifies the bearer JWT and puts the subject in context. |
| Tenant membership | `control.authorize_membership(org, principal)` resolves the role before tenant context; RLS begins after. |
| Development | `AUTH_MODE=dev` mounts `POST /v1/dev/token` to mint HS256 tokens. It refuses to start if `ENVIRONMENT != development`. |

```bash
TOKEN=$(curl -s -X POST localhost:8080/v1/dev/token \
  -H 'Content-Type: application/json' \
  -d '{"principal_id":"user_local"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
curl -s localhost:8080/v1/orgs -H "Authorization: Bearer $TOKEN"
```

The dev token endpoint is unauthenticated by design. It is never mounted in OIDC mode, and the VPS compose override
defaults `AUTH_MODE` to `oidc` with an empty dev secret. Do not expose a dev-mode API.

**Deferred:** API keys (creation, auth, revocation), browser cookie sessions and CSRF. The API accepts bearer tokens
only. API keys need a key-scope design/ADR and a pre-tenant authentication function; the OIDC provider can issue
service-principal tokens for CI in the meantime.

## 2. Implemented endpoints

| Method | Path | Role |
|---|---|---|
| POST | `/v1/dev/token` | development only |
| GET, POST | `/v1/orgs` | any authenticated principal |
| GET, POST | `/v1/orgs/{org}/members` | member read; owner write |
| PATCH, DELETE | `/v1/orgs/{org}/members/{principal}` | owner |
| GET, POST | `/v1/orgs/{org}/projects` | viewer read; operator write |
| GET | `/v1/orgs/{org}/projects/{project}` | viewer |
| GET, POST | `/v1/orgs/{org}/projects/{project}/agents` | viewer read; operator write |
| GET | `.../agents/{agent}` | viewer |
| GET, POST | `.../agents/{agent}/versions` | viewer read; operator write |
| GET | `.../agents/{agent}/versions/{version}` | viewer |
| POST | `.../agents/{agent}/versions/{version}:publish` | operator |
| GET, POST | `.../workflows` | viewer read; operator write |
| GET | `.../workflows/{workflow}` | viewer |
| GET, POST | `.../workflows/{workflow}/versions` | viewer read; operator write |
| GET | `.../workflows/{workflow}/versions/{version}` | viewer |
| POST | `.../workflows/{workflow}/versions/{version}:publish` | operator |
| POST, GET | `.../executions` | viewer read; operator submit |
| GET | `.../executions/{execution}` | viewer |
| GET | `.../executions/{execution}/tasks`, `.../tasks/{task}` | viewer |
| GET | `.../executions/{execution}/events` | viewer |

The full shapes are in [`api/openapi.yaml`](api/openapi.yaml). Authentication is required on every `/v1` route except
`/v1/meta` and the development token endpoint.

**Not implemented yet:** cancellation, SSE streaming, credentials, worker fleet endpoints. Cancellation and SSE
arrive with the scheduler/event fan-out; credentials with task-scoped credential delivery. Calls to unregistered
paths return `404`; this is recorded here so it is not mistaken for a contract violation.

## 3. Organisations, projects and members

- Any authenticated principal may create an organisation and becomes its first `owner`. This is an additive
  extension to the Phase 01 API contract; platform-operator provisioning still works.
- Member roles are `owner`, `operator`, `viewer`; the last owner cannot be removed or demoted.
- Members are added by OIDC `principal_id`. **Invitation email flows are deferred**; adding a member requires the
  principal to exist at the IdP.
- Projects are scoped by `(org_id, slug)`; IDs in a path are always filtered by the route org and RLS.
- **Environments are deferred.** Projects are the only scope in the accepted architecture.

## 4. Agent registry

Agent creation inserts the definition and an immutable draft version 1. New versions are added with
`POST .../versions`; there is no edit-in-place endpoint. Content and digest are protected by the database trigger
from Phase 03. The spec contains:

`model {provider, name, credential_ref, parameters}`, `instructions`, `tools[{tool_id, version, config}]`,
`runtime {abi_version, image_digest}`, `limits {max_turns, timeout_seconds}`, `checkpoint {mode,
require_checkpoint}`, and optional `output_schema`.

The version digest is SHA-256 over RFC 8785 canonical JSON of `{spec, runtime_abi}`. A duplicate version content
returns `409`. Publishing is `draft → published`; repeated publish is idempotent; deprecated versions cannot be
republished.

Publish-time worker capability and credential-existence checks are **deferred** until the worker fleet and credentials
APIs exist. The spec is still validated and immutable, and the credential reference is a name, never a secret.

## 5. Workflow registry

- Workflow creation inserts the definition and an immutable draft version with the validated graph.
- Creating a version reuses the shared `packages/contracts` validation: unique node IDs, agent/tool node shape,
  edge references, DAG/cycle detection, upstream-only `input_from`, retry safety, and size limits.
- The workflow `input_schema` and every node `output_schema` are compiled at creation and publish time. A malformed
  schema is `422`; the orchestrator re-validates execution input against `input_schema` before creating any row.
- Referenced **agent versions must exist** in the project at draft creation and must be `published` at workflow
  publish time. **Tool IDs are not registry-checked**: Phase 1 tools are platform-provided and ship with the runtime.
- Publishing is one-way. Editing always creates a new version; an execution pins one workflow version for life.
- **No archive/deprecate endpoint** yet; existing published versions remain usable. Adding deprecation is additive.
- CEL `when` expressions are validated for presence/length but **not compiled** in the API. Compilation belongs to
  publish-time validation in the control plane; adding cel-go is the next step for that check.

## 6. Execution submission

`POST .../executions` performs no AI work. It:

1. Authenticates the principal and resolves the organisation role.
2. Validates `Idempotency-Key`, input shape and the 256 KiB input limit.
3. Calls `orchestrator.SubmitExecution` over gRPC.
4. The orchestrator, in one transaction:
   - resolves the exact published workflow version and the pinned agent versions;
   - validates the execution input against the workflow's `input_schema` when present;
   - enforces per-project active/queued quotas and the execution duration cap;
   - inserts `executions` (`pending`, state version 1) with input and digests;
   - materialises one `execution_tasks` row per node and one `task_dependencies` row per edge;
   - appends the `execution.created` audit event with `seq = 1`;
   - inserts an unpublished `execution.outbox` row;
   - returns only after commit.
5. The API reads the committed execution and returns `202` with `Location` and counts.

Idempotency is unique on `(org_id, request_id)`. The request digest covers workflow, version, input and requested
deadline. An identical replay returns the original execution (`replayed=true`); a different body returns `409
idempotency_conflict`. A duplicate concurrent insert is rejected by the unique constraint.

No scheduler runs in Phase 04, so every task is `pending` and every execution is `pending`. That is the expected
state; Phase 05 owns assignment, leases and completion. The execution remains durable even if the API or
orchestrator restarts because it exists in PostgreSQL with its event and outbox rows.

## 7. Rate limits and quotas

| Limit | Value / location |
|---|---|
| API requests | `API_RATE_LIMIT_PER_MINUTE` (120) with burst `API_RATE_LIMIT_BURST` (30), per principal, in-memory |
| Active executions per project | `ORCHESTRATOR_MAX_ACTIVE_EXECUTIONS` (20), checked in the submit transaction |
| Queued executions per project | `ORCHESTRATOR_MAX_QUEUED_EXECUTIONS` (100) |
| Maximum execution duration | `ORCHESTRATOR_MAX_EXECUTION_SECONDS` (86400); later deadlines are rejected |
| Maximum request body | 1 MiB at the HTTP boundary |
| Maximum execution input | 256 KiB |
| Maximum workflow graph | 500 nodes / 2000 edges / 4 MiB database limit |
| Database quotas | Per-project counts in the same transaction that inserts the execution |

The in-memory rate limiter is an operational control: it resets on restart and each API replica has its own bucket.
The execution quotas are database-enforced and therefore consistent across API replicas.

## 8. Security and tenancy

- Every tenant query runs in a transaction with `SET LOCAL app.tenant_id`; a missing setting returns no rows.
- The API role is RLS-bound. The orchestrator role bypasses RLS only for cross-tenant scheduling.
- Cross-tenant paths return `404`, not `403`.
- Viewer/operator/owner matrix is enforced in handlers; organisation membership is resolved once per request.
- Bodies are limited, errors have a stable shape and request IDs, and internal errors do not leak SQL.
- Bearer tokens are never logged; the dev token endpoint is development-only.
- Credentials are not implemented, so no secret values can be written or read through this API yet.

## 9. Verification

```bash
make test                 # Go unit tests, Python and SDK suites
make lint                 # Go, Python, dashboard; includes integration-tagged Go files
make integration-test     # builds/starts the control plane and runs:
                          #   infrastructure, schema/RLS/immutability, outbox and full API flow tests
```

`tests/integration/api_flow_test.go` proves the exit criterion: dev/authenticated token → organisation → project →
agent version → publish → workflow version → publish → execution, then verifies durability in Postgres, idempotent
replay, idempotency conflict, RBAC denial and cross-tenant 404.

## 10. Deferred from the phase brief

| Brief item | Status | Reason / trigger |
|---|---|---|
| Registration, login, logout, password hashing, sessions | Delegated to OIDC | The architecture mandates OIDC; the platform has no local user/password table. |
| API keys | Deferred | Needs key scopes and a pre-tenant authentication design; OIDC service tokens cover CI. |
| Invitations | Partial | Members are added by principal ID; email delivery is an external service decision. |
| Environments | Deferred | Projects are the only scope in the accepted architecture. |
| Workflow archive | Deferred | Published versions are immutable; deprecation/archive is additive. |
| Required start node / end conditions | Not applicable | Roots/terminals are implicit per ADR 0007. |
| CEL compilation | Deferred | String is stored/validated; compilation is publish-time control-plane work. |
| Cancellation / SSE | Deferred | Depends on the scheduler, reaper and event publisher in Phase 05. |
| Credentials / worker fleet | Deferred | Runtime credential delivery and fleet management are later slices. |
