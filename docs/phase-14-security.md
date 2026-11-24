# Phase 14 — Security, Isolation and Governance

Status: complete for the single-host Compose deployment. The platform now has explicit permission groups, encrypted
project secrets, an append-only tenant audit trail, per-attempt OS limits, worker-control identities, network
segmentation, MCP egress policy and a security integration suite.

## 1. Multi-tenant isolation

Existing PostgreSQL RLS on control and execution tables remains the primary hard boundary. Every API request
resolves a membership before touching a tenant route, and every query runs through `database.WithTenant`
(`SET LOCAL app.tenant_id`).

Phase 14 additionally fixes a project-scope escape: a project-scoped API key could reach another project in the
**same** organisation because the route middleware read `project_id` before chi populated nested params. The
middleware now extracts the project segment from the request path as well, so `404` is returned before any
handler runs.

`TestTenantIsolationAcrossResources` attempts reads and mutations against projects, environments, agents,
workflows, versions, executions, tasks, events, tools, MCP, secrets, members, API keys, usage, workers and
approvals using a different tenant's admin key. It also proves a project-scoped key cannot cross into a sibling
project.

## 2. RBAC

Roles are now `owner`, `admin`, `developer`, `operator` (legacy alias for developer) and `viewer`.

| Permission | Owner | Admin | Developer | Viewer |
|---|---|---|---|---|
| Manage organisation / members | yes | yes (not ownership) | no | no |
| Create agents / deploy workflows | yes | yes | yes | no |
| View executions | yes | yes | yes | yes |
| Cancel executions | yes | yes | yes | no |
| Manage secrets | yes | yes | no | no |
| Manage billing/quotas | yes | scoped | no | no |

Admins cannot grant, demote or remove the owner role; only an owner can change ownership. Project-scoped keys
cannot manage organisation API keys or members. `TestRBACPermissionGroups` exercises the matrix with real
owner/admin/developer/viewer identities.

## 3. Secrets

`control.credentials` already stored ciphertext, nonce and key id. Phase 14 adds:

- `POST/GET/DELETE /api/v1/orgs/{org}/projects/{project}/secrets`
- `POST .../secrets/{id}:rotate` (omit `value` to re-encrypt under the active key)
- optional `environment_id`, descriptions, versions, last-access and revocation metadata
- AES-256-GCM envelope encryption through `internal/platform/secretbox`
- active + previous keyring support (`SECRETS_MASTER_KEY`, `SECRETS_ACTIVE_KEY_ID`, `SECRETS_PREVIOUS_KEYS`)
- tenant/row binding as AES-GCM associated data
- admin-only management, project-scope enforcement and `503` when no keyring is configured

Dashboard API keys are now kept only in tab memory; the persisted workspace store was migrated to drop previously
stored keys. Plaintext never leaves the API after creation except through the fenced worker credential RPC. The worker asks
for the credential names referenced by the pinned agent spec, the orchestrator resolves them after validating
the attempt lease, and the values travel only over the per-attempt Unix socket runtime call. They are not written
to NATS, execution events, checkpoints, logs or browser storage.

Agent validation rejects obvious credentials in model parameters, tool config and MCP `env`; MCP credentials must
use `auth_credential_ref`.

## 4. Runtime isolation

The worker image already ran as uid `10001`. The Compose worker now also has:

- read-only root filesystem with ephemeral `/tmp` and `/home/app` tmpfs
- all Linux capabilities dropped and `no-new-privileges`
- CPU, memory and PID limits
- per-attempt `prlimit` bounds for address space, CPU seconds, processes, open files, file size and core dumps
- `WORKER_REQUIRE_SANDBOX=true`, so startup fails closed if the limiter is unavailable
- an explicit environment allowlist; the runtime no longer inherits worker tokens, NATS URLs or control-plane
  configuration
- `cmd.Dir` set to the per-attempt work directory

MCP `stdio` is disabled by default at runtime and requires `AGENT_MCP_ALLOW_STDIO=true`. MCP HTTP endpoints are
checked by a URL policy that rejects credentials in URLs, metadata hosts, loopback, private, link-local,
multicast and reserved address space unless `AGENT_MCP_ALLOW_PRIVATE_NETWORKS=true`.

`TestWorkerRuntimeIsolation` checks non-root execution, zero effective capabilities, read-only root and that the
worker cannot connect to PostgreSQL across the network boundary.

## 5. Network security

Compose uses three networks:

- `state` — PostgreSQL, Redis, MinIO, NATS, orchestrator, API and migration job
- `work` — NATS, orchestrator and worker only
- `edge` — public API and dashboard

The worker/runtime cannot route to PostgreSQL, Redis or object storage. Data-plane host ports bind to
`127.0.0.1`; only the API and dashboard remain externally bound. NATS requires user/password authentication and
the worker's runtime process never receives those credentials. Worker-control RPCs require `x-worker-token`;
API command RPCs require `x-internal-token`. Lease-scoped delegation RPCs are token-free
because the runtime does not receive the worker token, and those RPCs validate the attempt lease themselves.
TLS is supported for gRPC; production HTTP terminates TLS at the ingress proxy.

## 6. Audit

`control.audit_events` is tenant-RLS protected and append-only for the application roles. A mutating-request
middleware records every organisation-scoped write with actor, action, resource, outcome and sanitised request
metadata. Explicit events cover secret metadata reads and worker secret resolution.

Covered actions include member/permission changes, API-key create/revoke, agent/workflow version and deploy
operations, execution create/cancel/resume/replay, approval decisions, projects, environments and secrets.
`TestAuditTrailAppendOnly` proves a valid action is recorded and `UPDATE` is rejected by the database trigger.

Authentication failures remain visible in structured HTTP request logs; the OIDC identity provider owns login
attempt history in production.

## 7. Abuse controls

Existing controls retained and documented:

- per-principal API request rate and burst limits
- request body limits and max execution input size
- per-project active/queued execution quotas
- maximum execution duration
- per-tenant active task cap
- provider and tool concurrency caps
- agent `max_turns`, timeout, retry, delegation depth/children/tokens/duration budgets
- per-attempt CPU/memory/process/file limits

## 8. Configuration

New production variables:

```bash
SECRETS_MASTER_KEY=<base64 32 bytes>
SECRETS_ACTIVE_KEY_ID=primary
SECRETS_PREVIOUS_KEYS=old-id:<base64>
WORKER_CONTROL_TOKEN=<random shared worker token>
INTERNAL_API_TOKEN=<random shared API token>
WORKER_REQUIRE_SANDBOX=true
NATS_USER=<user>
NATS_PASSWORD=<password>
AGENT_MCP_ALLOW_STDIO=false
AGENT_MCP_ALLOW_PRIVATE_NETWORKS=false
```

Production also fails closed if the development secret key, worker/API tokens or permissive MCP flags are
re-used. Compose keeps development defaults and loopback-only data ports. `.env.example` documents every knob.

## 9. Known limits

- This is container + prlimit isolation, not a separate kernel. Regulated or high-risk untrusted code should run
  workers on gVisor/Kata or a dedicated node pool; the worker interface stays the same.
- Egress is segmented by container network, not a per-process network namespace inside the worker. MCP HTTP has
  an application-level SSRF policy; a production egress proxy should enforce the same policy at the network layer.
- The audit API is not exposed to the dashboard yet; audit rows are queryable by operators through PostgreSQL or a
  future compliance view.
- Secret values are resolved for the default project scope. Environment-scoped rows are stored and managed, but
  executions do not yet carry an environment ID into the worker credential request.
